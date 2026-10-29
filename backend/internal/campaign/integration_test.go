//go:build integration

package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/content"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/llm"
)

// These tests run the campaign flow with the mock LLM against the seeded
// database (make test-integration). Created campaigns are deleted afterwards.

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

func createForTest(t *testing.T, svc *Service, db *pgxpool.Pool, channels []string) Campaign {
	t.Helper()
	c, err := svc.Create(context.Background(), exampleBrief, channels)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(context.Background(), "DELETE FROM campaigns WHERE id = $1", c.ID) })
	return c
}

func TestIntegrationFlow(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc := NewService(NewPGStore(db), &llm.Mock{})
	c := createForTest(t, svc, db, []string{"sms", "email"})

	c, err := svc.GenerateSegment(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusAudienceReady || c.Audience == nil || c.Audience.Matched == 0 ||
		c.Audience.SMS > c.Audience.Matched || c.Audience.Email > c.Audience.Matched {
		t.Fatalf("after segment: status %s, audience %+v", c.Status, c.Audience)
	}

	c, err = svc.GenerateContent(ctx, c.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusContentReady || len(c.Contents) != 2 || c.Contents[1].Subject == "" || c.Contents[0].Version != 1 {
		t.Fatalf("after content: %+v", c)
	}

	c, err = svc.EditContent(ctx, c.ID, "sms", Edit{Body: "{{first_name}}, %30 indirim bugün bitiyor!"})
	if err != nil {
		t.Fatal(err)
	}
	if sms := c.Contents[0]; !sms.HumanEdited || sms.Version != 2 || c.Contents[1].HumanEdited {
		t.Fatalf("after edit: %+v", c.Contents)
	}

	// Regenerating content resets the edit flag and bumps the version.
	if c, err = svc.GenerateContent(ctx, c.ID, nil); err != nil || c.Contents[0].HumanEdited || c.Contents[0].Version != 3 {
		t.Fatalf("regenerated: %+v, err = %v", c.Contents, err)
	}

	var kinds []string
	var parsed, validationErrors []byte
	rows, err := db.Query(ctx, `SELECT kind, parsed, validation_errors FROM llm_generations
		WHERE campaign_id = $1 ORDER BY id`, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind, &parsed, &validationErrors); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, kind)
	}
	if strings.Join(kinds, ",") != "segment,content,content" || !json.Valid(parsed) || validationErrors != nil {
		t.Fatalf("llm_generations: kinds %v, parsed %s, validation_errors %s", kinds, parsed, validationErrors)
	}
}

func TestIntegrationRecordsInvalidAttempts(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	bad := llm.ScriptedAnswer{Text: `{"match": "all", "conditions": [{"field": "income", "op": "eq", "value": "x"}]}`}
	svc := NewService(NewPGStore(db), llm.NewScripted(bad, bad, bad))
	c := createForTest(t, svc, db, []string{"sms"})

	if _, err := svc.GenerateSegment(ctx, c.ID); err == nil {
		t.Fatal("want a generation error")
	}
	var n int
	var path string
	err := db.QueryRow(ctx, `SELECT count(*), min(validation_errors->0->>'path') FROM llm_generations
		WHERE campaign_id = $1 AND NOT valid AND parsed IS NULL`, c.ID).Scan(&n, &path)
	if err != nil || n != 1+llm.MaxRepairs || path != "conditions[0].field" {
		t.Fatalf("invalid attempts = %d, first path %q, err %v", n, path, err)
	}
	if c, _ = svc.Get(ctx, c.ID); c.Status != StatusDraft || c.LastError == "" {
		t.Fatalf("campaign = %+v", c)
	}
}

func TestIntegrationMaxLengths(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	store := NewPGStore(db)
	svc := NewService(store, &llm.Mock{})
	c := createForTest(t, svc, db, []string{"sms"})
	c, err := svc.GenerateSegment(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}

	lens, err := store.MaxLengths(ctx, *c.Rule, c.Channels, svc.now())
	if err != nil {
		t.Fatal(err)
	}
	// The rule targets İstanbul, so the longest city is exactly "İstanbul"
	// (8 characters, not 9 bytes); names are at most "Muhammed Emin" (13).
	if lens[content.VarCity] != 8 || lens[content.VarFirstName] < 3 || lens[content.VarFirstName] > 13 {
		t.Fatalf("lengths = %v", lens)
	}
}

func TestIntegrationWritesStopAfterApproval(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	store := NewPGStore(db)
	svc := NewService(store, &llm.Mock{})
	c := createForTest(t, svc, db, []string{"sms"})
	if _, err := svc.GenerateSegment(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	c, err := svc.GenerateContent(ctx, c.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "UPDATE campaigns SET status = 'approved', approved_at = now() WHERE id = $1", c.ID); err != nil {
		t.Fatal(err)
	}

	// The store itself refuses, even if a caller skipped the service checks.
	sms := content.Content{SMS: &content.SMS{Body: "changed"}}
	for name, err := range map[string]error{
		"SaveSegment": store.SaveSegment(ctx, c.ID, *c.Rule, *c.Audience),
		"SaveContent": store.SaveContent(ctx, c.ID, sms),
		"EditContent": store.EditContent(ctx, c.ID, sms, "sms"),
	} {
		if !errors.Is(err, ErrConflict) {
			t.Errorf("%s: err = %v, want ErrConflict", name, err)
		}
	}
	if got, _ := svc.Get(ctx, c.ID); got.Contents[0].Body != c.Contents[0].Body {
		t.Fatal("approved content changed")
	}
}
