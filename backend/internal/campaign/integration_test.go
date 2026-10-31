//go:build integration

package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/content"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/llm"
)

const exampleBrief = "Son 30 günde sepete ürün ekleyip satın almayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder"

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

	lens, err := store.MaxLengths(ctx, *c.Rule, c.Channels, time.Now())
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

func TestIntegrationApproveEnqueues(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc := NewService(NewPGStore(db), &llm.Mock{})
	c := createForTest(t, svc, db, []string{"sms", "email"})
	if _, err := svc.GenerateSegment(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GenerateContent(ctx, c.ID, nil); err != nil {
		t.Fatal(err)
	}

	c, err := svc.Approve(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusSending || c.ApprovedAt == nil {
		t.Fatalf("status %s, approved_at %v", c.Status, c.ApprovedAt)
	}

	// One queued message per user and channel, matching the audience counts,
	// and only to users who consented to that channel.
	var sms, email, queued, unconsented int
	err = db.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE m.channel = 'sms'),
		       count(*) FILTER (WHERE m.channel = 'email'),
		       count(*) FILTER (WHERE m.status = 'queued' AND m.attempts = 0),
		       count(*) FILTER (WHERE (m.channel = 'sms' AND NOT u.sms_opt_in) OR (m.channel = 'email' AND NOT u.email_opt_in))
		FROM messages m JOIN users u ON u.id = m.user_id WHERE m.campaign_id = $1`, c.ID).
		Scan(&sms, &email, &queued, &unconsented)
	if err != nil {
		t.Fatal(err)
	}
	if sms != c.Audience.SMS || email != c.Audience.Email || queued != sms+email || unconsented != 0 || sms == 0 || email == 0 {
		t.Fatalf("sms %d (want %d), email %d (want %d), queued %d, unconsented %d",
			sms, c.Audience.SMS, email, c.Audience.Email, queued, unconsented)
	}

	// Enqueueing again adds nothing.
	err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		n, err := enqueue(ctx, tx, c.ID, *c.Rule, "sms", time.Now())
		if err == nil && n != 0 {
			err = fmt.Errorf("re-enqueue inserted %d messages", n)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Approve(ctx, c.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("second approve: err = %v", err)
	}
}

func TestIntegrationApproveUnreachableAudience(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc := NewService(NewPGStore(db), &llm.Mock{})
	c := createForTest(t, svc, db, []string{"sms"})
	if _, err := svc.GenerateSegment(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GenerateContent(ctx, c.ID, nil); err != nil {
		t.Fatal(err)
	}
	// The audience is recomputed at approval; simulate it having emptied.
	_, err := db.Exec(ctx, `UPDATE campaigns SET segment_rule = '{"match": "all", "conditions": [{"field": "city", "op": "eq", "value": "Nowhere"}]}' WHERE id = $1`, c.ID)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Approve(ctx, c.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	var n int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM messages WHERE campaign_id = $1", c.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("messages = %d, err %v", n, err)
	}
	if c, _ = svc.Get(ctx, c.ID); c.Status != StatusContentReady || c.ApprovedAt != nil {
		t.Fatalf("status %s, approved_at %v", c.Status, c.ApprovedAt)
	}
}

func TestIntegrationProgressAndList(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc := NewService(NewPGStore(db), &llm.Mock{})
	c := createForTest(t, svc, db, []string{"sms", "email"})
	if _, err := svc.GenerateSegment(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GenerateContent(ctx, c.ID, nil); err != nil {
		t.Fatal(err)
	}
	if p, err := svc.Progress(ctx, c.ID); err != nil || p.Totals.Total != 0 || len(p.Channels) != 2 || p.Done {
		t.Fatalf("before approval: %+v, err %v", p, err)
	}
	c, err := svc.Approve(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate some delivery: two sent, three failed with two reasons.
	_, err = db.Exec(ctx, `
		WITH picked AS (SELECT id, row_number() OVER (ORDER BY id) AS n FROM messages WHERE campaign_id = $1 AND channel = 'sms')
		UPDATE messages m SET
		    status = CASE WHEN p.n <= 2 THEN 'sent' ELSE 'failed' END,
		    sent_at = CASE WHEN p.n <= 2 THEN now() END,
		    last_error = CASE WHEN p.n IN (3, 4) THEN 'provider returned 400: bad phone' WHEN p.n = 5 THEN 'lease expired' END
		FROM picked p WHERE m.id = p.id AND p.n <= 5`, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.Progress(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	sms, email := p.Channels["sms"], p.Channels["email"]
	if sms.Total != c.Audience.SMS || sms.Sent != 2 || sms.Failed != 3 || sms.Queued != c.Audience.SMS-5 ||
		email.Total != c.Audience.Email || email.Queued != email.Total || p.Totals.Total != sms.Total+email.Total {
		t.Fatalf("progress = %+v (audience %+v)", p, c.Audience)
	}
	if len(p.Failures) != 2 || p.Failures[0] != (FailureReason{Error: "provider returned 400: bad phone", Count: 2}) {
		t.Fatalf("failures = %+v", p.Failures)
	}

	list, err := svc.List(ctx, 200, 0)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(list, func(s Summary) bool { return s.ID == c.ID })
	if i < 0 || list[i].Messages != p.Totals || list[i].Status != StatusSending || list[i].ApprovedAt == nil {
		t.Fatalf("list entry = %+v, want messages %+v", list, p.Totals)
	}
	for j := 1; j < len(list); j++ {
		if list[j].ID >= list[j-1].ID {
			t.Fatal("list is not newest first")
		}
	}
	if older, err := svc.List(ctx, 1, c.ID); err != nil || len(older) == 1 && older[0].ID >= c.ID {
		t.Fatalf("before %d: %+v, err %v", c.ID, older, err)
	}
	if _, err := svc.Progress(ctx, -1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown campaign: %v", err)
	}
}

// Two approvals racing: the campaign row lock lets exactly one through, and
// every recipient is queued once per channel.
func TestIntegrationConcurrentApprovals(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc := NewService(NewPGStore(db), &llm.Mock{})
	c := createForTest(t, svc, db, []string{"sms", "email"})
	if _, err := svc.GenerateSegment(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GenerateContent(ctx, c.ID, nil); err != nil {
		t.Fatal(err)
	}

	const n = 5
	errs := make(chan error, n)
	start := make(chan struct{})
	for range n {
		go func() {
			<-start
			_, err := svc.Approve(ctx, c.ID)
			errs <- err
		}()
	}
	close(start)
	ok := 0
	for range n {
		switch err := <-errs; {
		case err == nil:
			ok++
		case !errors.Is(err, ErrConflict):
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d approvals succeeded, want 1", ok)
	}

	c, _ = svc.Get(ctx, c.ID)
	var total, distinct int
	err := db.QueryRow(ctx, `SELECT count(*), count(DISTINCT (user_id, channel)) FROM messages WHERE campaign_id = $1`, c.ID).Scan(&total, &distinct)
	if err != nil || total != distinct || total != c.Audience.SMS+c.Audience.Email {
		t.Fatalf("messages %d, distinct %d, want %d (err %v)", total, distinct, c.Audience.SMS+c.Audience.Email, err)
	}
}
