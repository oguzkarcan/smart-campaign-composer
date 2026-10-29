package segment

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

var compileNow = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

func mustParse(t *testing.T, raw string) Rule {
	t.Helper()
	r, err := Parse([]byte(raw), testCatalog)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestWhereExampleBrief(t *testing.T) {
	where, args, err := Where(mustParse(t, exampleBrief), compileNow)
	if err != nil {
		t.Fatal(err)
	}

	wantSQL := "(u.city = $1" +
		" AND EXISTS (SELECT 1 FROM user_events e WHERE e.user_id = u.id AND e.type = $2 AND e.occurred_at >= $3)" +
		" AND NOT EXISTS (SELECT 1 FROM user_events e WHERE e.user_id = u.id AND e.type = $4 AND e.occurred_at >= $5))"
	if where != wantSQL {
		t.Fatalf("where =\n%s\nwant\n%s", where, wantSQL)
	}
	thirtyDaysAgo := compileNow.Add(-30 * 24 * time.Hour)
	wantArgs := []any{"İstanbul", "cart_add", thirtyDaysAgo, "purchase", thirtyDaysAgo}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %v, want %v", args, wantArgs)
	}
}

func TestWhereOperators(t *testing.T) {
	tests := []struct {
		cond     string
		wantSQL  string
		wantArgs []any
	}{
		{`{"field":"city","op":"neq","value":"Ankara"}`, "(u.city <> $1)", []any{"Ankara"}},
		{`{"field":"city","op":"in","value":["Ankara","İzmir"]}`, "(u.city = ANY($1))", []any{[]string{"Ankara", "İzmir"}}},
		{`{"field":"language","op":"not_in","value":["en"]}`, "(u.language <> ALL($1))", []any{[]string{"en"}}},
		{
			`{"event":"purchase","within_days":90,"exists":true,"min_count":3}`,
			"((SELECT count(*) FROM user_events e WHERE e.user_id = u.id AND e.type = $1 AND e.occurred_at >= $2) >= $3)",
			[]any{"purchase", compileNow.Add(-90 * 24 * time.Hour), 3},
		},
	}
	for _, tt := range tests {
		t.Run(tt.cond, func(t *testing.T) {
			where, args, err := Where(mustParse(t, `{"match":"all","conditions":[`+tt.cond+`]}`), compileNow)
			if err != nil {
				t.Fatal(err)
			}
			if where != tt.wantSQL || !reflect.DeepEqual(args, tt.wantArgs) {
				t.Fatalf("got %s %v, want %s %v", where, args, tt.wantSQL, tt.wantArgs)
			}
		})
	}
}

func TestWhereNestedGroupsAndEmptyRule(t *testing.T) {
	r := mustParse(t, `{"match":"any","conditions":[
		{"field":"city","op":"eq","value":"Ankara"},
		{"match":"all","conditions":[{"field":"language","op":"eq","value":"en"},{"field":"city","op":"eq","value":"İzmir"}]}
	]}`)
	where, _, err := Where(r, compileNow)
	if err != nil {
		t.Fatal(err)
	}
	if want := "(u.city = $1 OR (u.language = $2 AND u.city = $3))"; where != want {
		t.Fatalf("where = %s, want %s", where, want)
	}

	where, args, err := Where(Rule{Match: MatchAll}, compileNow)
	if err != nil || where != "TRUE" || len(args) != 0 {
		t.Fatalf("empty rule: got %q %v %v, want TRUE", where, args, err)
	}
}

// Even a rule that skipped validation cannot put anything but whitelisted
// names into SQL; values only ever become arguments.
func TestWhereNeverInterpolatesInput(t *testing.T) {
	evil := "x' OR '1'='1'; DROP TABLE users; --"
	exists := true

	_, args, err := Where(Rule{Match: MatchAll, Conditions: []Condition{{Field: "city", Op: OpEq, Value: Values{evil}}}}, compileNow)
	if err != nil {
		t.Fatal(err)
	}
	if args[0] != evil {
		t.Fatalf("value should be passed as an argument, got %v", args)
	}

	for name, c := range map[string]Condition{
		"field": {Field: evil, Op: OpEq, Value: Values{"a"}},
		"op":    {Field: "city", Op: evil, Value: Values{"a"}},
		"event": {Event: evil, WithinDays: 1, Exists: &exists},
		"match": {Match: evil, Conditions: []Condition{{Field: "city", Op: OpEq, Value: Values{"a"}}}},
	} {
		if where, _, err := Where(Rule{Match: MatchAll, Conditions: []Condition{c}}, compileNow); err == nil {
			t.Errorf("%s: unvalidated name reached SQL: %s", name, where)
		}
	}
}

func TestRecipientsSQLEnforcesConsentOutsideTheRule(t *testing.T) {
	r := mustParse(t, `{"match":"any","conditions":[
		{"field":"city","op":"eq","value":"Ankara"},
		{"field":"language","op":"eq","value":"en"}
	]}`)
	for channel, consent := range map[string]string{ChannelSMS: "u.sms_opt_in", ChannelEmail: "u.email_opt_in"} {
		sql, _, err := RecipientsSQL(r, channel, compileNow)
		if err != nil {
			t.Fatal(err)
		}
		// The OR stays inside parentheses, so consent applies to every branch.
		want := "WHERE ((u.city = $1 OR u.language = $2)) AND " + consent + " ORDER BY u.id"
		if !strings.HasSuffix(sql, want) {
			t.Fatalf("%s: sql = %s, want suffix %s", channel, sql, want)
		}
	}
	if _, _, err := RecipientsSQL(r, "whatsapp", compileNow); err == nil {
		t.Fatal("unknown channel must be rejected")
	}
}
