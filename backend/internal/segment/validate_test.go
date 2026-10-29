package segment

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

var testCatalog = Catalog{
	"city":     {"Ankara", "İstanbul", "İzmir", "Eskişehir"},
	"language": {"en", "tr"},
}

// exampleBrief is the rule for the case's example: İstanbul users who added to
// cart in the last 30 days and did not purchase.
const exampleBrief = `{"match": "all", "conditions": [
	{"field": "city", "op": "eq", "value": "İstanbul"},
	{"event": "cart_add", "within_days": 30, "exists": true},
	{"event": "purchase", "within_days": 30, "exists": false}
]}`

func TestParseValidRules(t *testing.T) {
	tests := map[string]string{
		"example brief": exampleBrief,
		"everyone":      `{"match": "all", "conditions": []}`,
		"in list":       `{"match": "all", "conditions": [{"field": "city", "op": "in", "value": ["Ankara", "İzmir"]}]}`,
		"min count":     `{"match": "all", "conditions": [{"event": "purchase", "within_days": 90, "exists": true, "min_count": 3}]}`,
		"nested any": `{"match": "all", "conditions": [
			{"field": "language", "op": "eq", "value": "tr"},
			{"match": "any", "conditions": [
				{"field": "city", "op": "eq", "value": "Ankara"},
				{"event": "page_view", "within_days": 7, "exists": true}
			]}
		]}`,
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(raw), testCatalog); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestParseCanonicalizesValues(t *testing.T) {
	r, err := Parse([]byte(`{"match": "all", "conditions": [
		{"field": "city", "op": "in", "value": ["istanbul", "ISTANBUL", " Eskisehir "]}
	]}`), testCatalog)
	if err != nil {
		t.Fatal(err)
	}
	want := Values{"İstanbul", "Eskişehir"}
	if got := r.Conditions[0].Value; !reflect.DeepEqual(got, want) {
		t.Fatalf("values = %q, want %q (canonical, deduplicated)", got, want)
	}
}

func TestParseRejects(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantPath string
		wantMsg  string
	}{
		{"not JSON", `SELECT * FROM users`, "", "invalid JSON"},
		{"trailing data", `{"match":"all","conditions":[]} {}`, "", "unexpected data"},
		{"trailing brace", `{"match":"all","conditions":[]}}`, "", "unexpected data"},
		{"trailing bracket", `{"match":"all","conditions":[]}]`, "", "unexpected data"},
		{"trailing text", `{"match":"all","conditions":[]} done`, "", "unexpected data"},
		{"unknown top-level key", `{"match":"all","conditions":[],"sql":"DROP TABLE users"}`, "", "unknown field"},
		{"unknown condition key", `{"match":"all","conditions":[{"field":"city","op":"eq","value":"Ankara","raw":"1=1"}]}`, "", "unknown field"},
		{"bad match", `{"match":"some","conditions":[]}`, "match", "must be"},
		{"unknown field", `{"match":"all","conditions":[{"field":"password","op":"eq","value":"x"}]}`, "conditions[0].field", "unknown field"},
		{"column injection", `{"match":"all","conditions":[{"field":"city; DROP TABLE users","op":"eq","value":"x"}]}`, "conditions[0].field", "unknown field"},
		{"unknown op", `{"match":"all","conditions":[{"field":"city","op":"like","value":"%"}]}`, "conditions[0].op", "unknown op"},
		{"eq with two values", `{"match":"all","conditions":[{"field":"city","op":"eq","value":["Ankara","İzmir"]}]}`, "conditions[0].value", "exactly one"},
		{"in with no values", `{"match":"all","conditions":[{"field":"city","op":"in","value":[]}]}`, "conditions[0].value", "1-50"},
		{"number value", `{"match":"all","conditions":[{"field":"city","op":"eq","value":34}]}`, "", "string or an array"},
		{"unknown city", `{"match":"all","conditions":[{"field":"city","op":"eq","value":"Paris"}]}`, "conditions[0].value", "allowed: Ankara"},
		{"value injection", `{"match":"all","conditions":[{"field":"city","op":"eq","value":"x' OR '1'='1"}]}`, "conditions[0].value", "unknown city"},
		{"unknown event", `{"match":"all","conditions":[{"event":"refund","within_days":30,"exists":true}]}`, "conditions[0].event", "unknown event"},
		{"within_days missing", `{"match":"all","conditions":[{"event":"purchase","exists":true}]}`, "conditions[0].within_days", "between 1 and 365"},
		{"within_days too big", `{"match":"all","conditions":[{"event":"purchase","within_days":9999,"exists":true}]}`, "conditions[0].within_days", "between 1 and 365"},
		{"exists missing", `{"match":"all","conditions":[{"event":"purchase","within_days":30}]}`, "conditions[0].exists", "required"},
		{"min_count without exists", `{"match":"all","conditions":[{"event":"purchase","within_days":30,"exists":false,"min_count":2}]}`, "conditions[0].min_count", "only allowed"},
		{"mixed kinds", `{"match":"all","conditions":[{"field":"city","event":"purchase","op":"eq","value":"Ankara"}]}`, "conditions[0]", "exactly one"},
		{"empty condition", `{"match":"all","conditions":[{}]}`, "conditions[0]", "exactly one"},
		{"event keys on field", `{"match":"all","conditions":[{"field":"city","op":"eq","value":"Ankara","within_days":3}]}`, "conditions[0]", "not allowed"},
		{"empty nested group", `{"match":"all","conditions":[{"match":"any","conditions":[]}]}`, "conditions[0].conditions", "at least one"},
		{"too deep", `{"match":"all","conditions":[{"match":"any","conditions":[{"match":"all","conditions":[{"match":"any","conditions":[{"field":"city","op":"eq","value":"Ankara"}]}]}]}]}`, "conditions[0].conditions[0].conditions[0]", "too deeply"},
		{"too many", `{"match":"all","conditions":[` + strings.TrimSuffix(strings.Repeat(`{"field":"city","op":"eq","value":"Ankara"},`, 21), ",") + `]}`, "", "too many conditions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.raw), testCatalog)
			var verr ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("got %v, want a ValidationError", err)
			}
			for _, p := range verr {
				if p.Path == tt.wantPath && strings.Contains(p.Message, tt.wantMsg) {
					return
				}
			}
			t.Fatalf("no problem at %q containing %q; got %v", tt.wantPath, tt.wantMsg, verr)
		})
	}
}

func TestParseReportsAllProblems(t *testing.T) {
	_, err := Parse([]byte(`{"match":"all","conditions":[
		{"field":"city","op":"eq","value":"Paris"},
		{"event":"purchase","within_days":0,"exists":true}
	]}`), testCatalog)
	var verr ValidationError
	if !errors.As(err, &verr) || len(verr) != 2 {
		t.Fatalf("want 2 problems, got %v", err)
	}
}
