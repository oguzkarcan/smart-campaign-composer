//go:build integration

package segment

import (
	"context"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests run compiled rules against the seeded database (make
// test-integration) and compare them with hand-written reference SQL.

func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestIntegrationCatalog(t *testing.T) {
	cat, err := LoadCatalog(context.Background(), testDB(t))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cat["city"], "İstanbul") || !slices.Equal(cat["language"], []string{"en", "tr"}) {
		t.Fatalf("catalog = %v", cat)
	}
}

func TestIntegrationCountMatchesReferenceSQL(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	cat, err := LoadCatalog(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()

	r, err := Parse([]byte(exampleBrief), cat)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Count(ctx, db, r, now)
	if err != nil {
		t.Fatal(err)
	}

	// Reference per language; the totals are their sum.
	rows, err := db.Query(ctx, `
		SELECT language, count(*), count(*) FILTER (WHERE sms_opt_in), count(*) FILTER (WHERE email_opt_in)
		FROM users u
		WHERE city = 'İstanbul'
		  AND EXISTS (SELECT 1 FROM user_events e WHERE e.user_id = u.id AND e.type = 'cart_add' AND e.occurred_at >= $1)
		  AND NOT EXISTS (SELECT 1 FROM user_events e WHERE e.user_id = u.id AND e.type = 'purchase' AND e.occurred_at >= $1)
		GROUP BY 1`,
		now.Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := Audience{Languages: map[string]Counts{}}
	var lang string
	var c Counts
	if _, err := pgx.ForEachRow(rows, []any{&lang, &c.Matched, &c.SMS, &c.Email}, func() error {
		want.Languages[lang] = c
		want.Matched, want.SMS, want.Email = want.Matched+c.Matched, want.SMS+c.SMS, want.Email+c.Email
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("compiled count = %+v, reference = %+v", got, want)
	}
	if got.Matched == 0 || got.SMS >= got.Matched || got.Email >= got.Matched || len(got.Languages) != 2 {
		t.Fatalf("implausible audience %+v", got)
	}
	t.Logf("example brief audience: %+v", got)
}

func TestIntegrationEmptyRuleMatchesEveryone(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	got, err := Count(ctx, db, Rule{Match: MatchAll}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var users int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&users); err != nil {
		t.Fatal(err)
	}
	if got.Matched != users {
		t.Fatalf("matched %d, want all %d users", got.Matched, users)
	}
}

func TestIntegrationRecipientsOnlyIncludeConsentedUsers(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	cat, err := LoadCatalog(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	// An "any" rule that matches most users: the case where a missing
	// parenthesis would let OR bypass the consent filter.
	r, err := Parse([]byte(`{"match":"any","conditions":[
		{"field":"language","op":"eq","value":"tr"},
		{"field":"language","op":"eq","value":"en"}
	]}`), cat)
	if err != nil {
		t.Fatal(err)
	}

	for channel, column := range map[string]string{ChannelSMS: "sms_opt_in", ChannelEmail: "email_opt_in"} {
		sql, args, err := RecipientsSQL(r, channel, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		rows, err := db.Query(ctx, sql, args...)
		if err != nil {
			t.Fatal(err)
		}
		ids, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (int64, error) {
			var id int64
			var lang string
			return id, row.Scan(&id, &lang)
		})
		if err != nil {
			t.Fatal(err)
		}

		var withoutConsent, consented int
		err = db.QueryRow(ctx, "SELECT count(*) FILTER (WHERE NOT "+column+"), count(*) FILTER (WHERE "+column+") FROM users WHERE id = ANY($1)", ids).
			Scan(&withoutConsent, &consented)
		if err != nil {
			t.Fatal(err)
		}
		var total int
		if err := db.QueryRow(ctx, "SELECT count(*) FROM users WHERE "+column).Scan(&total); err != nil {
			t.Fatal(err)
		}
		if withoutConsent != 0 || consented != total {
			t.Fatalf("%s: %d recipients without consent, %d of %d consented users", channel, withoutConsent, consented, total)
		}
	}
}
