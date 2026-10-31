//go:build integration

package delivery

import (
	"context"
	"errors"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/campaign"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/llm"
)

// These tests use the seeded compose database (make test-integration, which
// stops the compose worker so it does not take the test's messages).

const exampleBrief = "Son 30 günde sepete ürün ekleyip satın almayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder"

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

// approvedCampaign creates and approves a campaign with the mock LLM,
// deleting it (and its messages) afterwards. Claims take due messages of any
// campaign, so the test is skipped if others are waiting.
func approvedCampaign(t *testing.T, db *pgxpool.Pool, channels ...string) (campaign.Campaign, int) {
	t.Helper()
	ctx := context.Background()
	var others int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM messages WHERE status IN ('queued', 'retry', 'processing')").Scan(&others); err != nil {
		t.Fatal(err)
	}
	if others > 0 {
		t.Skipf("%d unfinished messages from other campaigns are in the queue", others)
	}

	svc := campaign.NewService(campaign.NewPGStore(db), &llm.Mock{})
	if len(channels) == 0 {
		channels = []string{"sms"}
	}
	c, err := svc.Create(ctx, exampleBrief, channels)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(context.Background(), "DELETE FROM campaigns WHERE id = $1", c.ID) })
	if _, err := svc.GenerateSegment(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GenerateContent(ctx, c.ID, nil); err != nil {
		t.Fatal(err)
	}
	if c, err = svc.Approve(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM messages WHERE campaign_id = $1", c.ID).Scan(&n); err != nil || n < 20 {
		t.Fatalf("queued %d messages, err %v", n, err)
	}
	return c, n
}

type messageRow struct {
	status, lastError, providerID string
	attempts                      int
	sent, leased                  bool
	nextIn                        time.Duration
}

func message(t *testing.T, db *pgxpool.Pool, id int64) messageRow {
	t.Helper()
	var m messageRow
	var lastError, providerID *string
	var nextIn float64
	err := db.QueryRow(context.Background(), `
		SELECT status, attempts, last_error, provider_message_id, sent_at IS NOT NULL, lease_until IS NOT NULL,
		       extract(epoch FROM next_attempt_at - now())
		FROM messages WHERE id = $1`, id).
		Scan(&m.status, &m.attempts, &lastError, &providerID, &m.sent, &m.leased, &nextIn)
	if err != nil {
		t.Fatal(err)
	}
	if lastError != nil {
		m.lastError = *lastError
	}
	if providerID != nil {
		m.providerID = *providerID
	}
	m.nextIn = time.Duration(nextIn * float64(time.Second))
	return m
}

func TestIntegrationQueue(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	c, total := approvedCampaign(t, db, "sms")
	q := NewPGQueue(db)

	// Concurrent claims never return the same message.
	var mu sync.Mutex
	var jobs []Job
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := q.Claim(ctx, "sms", 5, 30*time.Second)
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			jobs = append(jobs, got...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	ids := map[int64]bool{}
	for _, j := range jobs {
		if ids[j.MessageID] {
			t.Fatalf("message %d claimed twice", j.MessageID)
		}
		ids[j.MessageID] = true
	}
	if len(jobs) != 20 {
		t.Fatalf("claimed %d messages, want 20", len(jobs))
	}
	j := jobs[0]
	if j.CampaignID != c.ID || j.Channel != "sms" || j.Attempt != 1 || j.Phone == "" || j.FirstName == "" ||
		j.City != "İstanbul" || j.Body != c.Contents[0].Body || j.Subject != "" {
		t.Fatalf("job = %+v", j)
	}
	if m := message(t, db, j.MessageID); m.status != StatusProcessing || m.attempts != 1 || !m.leased {
		t.Fatalf("claimed message = %+v", m)
	}
	if more, _ := q.Claim(ctx, "email", 10, 30*time.Second); len(more) != 0 {
		t.Fatalf("claimed %d email messages from an SMS campaign", len(more))
	}

	// Sent.
	ok, err := q.Finish(ctx, jobs[0], Result{Outcome: Sent, StatusCode: 202, ProviderMessageID: "sms_1", Latency: 80 * time.Millisecond}, Decision{Status: StatusSent})
	if m := message(t, db, jobs[0].MessageID); err != nil || !ok || m.status != StatusSent || !m.sent || m.leased || m.providerID != "sms_1" || m.lastError != "" {
		t.Fatalf("sent: %+v, ok %v, err %v", m, ok, err)
	}

	// Retry: not claimable until its time.
	res := Result{Outcome: Retry, StatusCode: 429, RetryAfter: time.Hour, Err: errors.New("provider returned 429: rate limit exceeded")}
	ok, err = q.Finish(ctx, jobs[1], res, Decision{Status: StatusRetry, Delay: time.Hour})
	m := message(t, db, jobs[1].MessageID)
	if err != nil || !ok || m.status != StatusRetry || m.leased || m.nextIn < 59*time.Minute || m.lastError == "" {
		t.Fatalf("retry: %+v, ok %v, err %v", m, ok, err)
	}

	// Permanent failure.
	res = Result{Outcome: Permanent, StatusCode: 400, Err: errors.New("provider returned 400: bad phone")}
	if ok, err = q.Finish(ctx, jobs[2], res, Decision{Status: StatusFailed}); err != nil || !ok || message(t, db, jobs[2].MessageID).status != StatusFailed {
		t.Fatalf("failed: ok %v, err %v", ok, err)
	}

	// Lease expiry: recovered for a retry; the late result is recorded but
	// does not overwrite the message.
	if _, err := db.Exec(ctx, "UPDATE messages SET lease_until = now() - interval '1 second' WHERE id = $1", jobs[3].MessageID); err != nil {
		t.Fatal(err)
	}
	if n, err := q.RecoverLeases(ctx); err != nil || n != 1 {
		t.Fatalf("recovered %d, err %v", n, err)
	}
	if m := message(t, db, jobs[3].MessageID); m.status != StatusRetry || m.leased || m.attempts != 1 || m.nextIn > 0 {
		t.Fatalf("recovered: %+v", m)
	}
	if ok, err = q.Finish(ctx, jobs[3], Result{Outcome: Sent, StatusCode: 202}, Decision{Status: StatusSent}); err != nil || ok {
		t.Fatalf("stale finish: ok %v, err %v", ok, err)
	}
	if m := message(t, db, jobs[3].MessageID); m.status != StatusRetry {
		t.Fatalf("stale finish changed the message: %+v", m)
	}

	// A lease that expires on the last attempt fails the message.
	if _, err := db.Exec(ctx, "UPDATE messages SET attempts = $2, lease_until = now() - interval '1 second' WHERE id = $1", jobs[4].MessageID, MaxAttempts); err != nil {
		t.Fatal(err)
	}
	if _, err := q.RecoverLeases(ctx); err != nil || message(t, db, jobs[4].MessageID).status != StatusFailed {
		t.Fatalf("lease expired on the last attempt: err %v, %+v", err, message(t, db, jobs[4].MessageID))
	}

	// Release gives the attempt back.
	if err := q.Release(ctx, jobs[5:]); err != nil {
		t.Fatal(err)
	}
	if m := message(t, db, jobs[5].MessageID); m.status != StatusQueued || m.attempts != 0 || m.leased {
		t.Fatalf("released: %+v", m)
	}

	// Every provider call is in message_attempts, including the stale one.
	var recorded int
	var status *int
	err = db.QueryRow(ctx, `SELECT count(*), max(http_status) FILTER (WHERE message_id = $2)
		FROM message_attempts a JOIN messages m ON m.id = a.message_id WHERE m.campaign_id = $1`, c.ID, jobs[1].MessageID).Scan(&recorded, &status)
	if err != nil || recorded != 4 || status == nil || *status != 429 {
		t.Fatalf("attempts recorded %d, 429 recorded %v, err %v", recorded, status, err)
	}

	// The campaign finishes only when nothing is left to send.
	if done, err := q.FinishCampaigns(ctx); err != nil || slices.ContainsFunc(done, func(d CampaignDone) bool { return d.ID == c.ID }) {
		t.Fatalf("finished early: %v, err %v", done, err)
	}
	if _, err := db.Exec(ctx, "UPDATE messages SET status = 'failed', lease_until = NULL WHERE campaign_id = $1 AND status <> 'sent'", c.ID); err != nil {
		t.Fatal(err)
	}
	done, err := q.FinishCampaigns(ctx)
	if err != nil || !slices.Contains(done, CampaignDone{ID: c.ID, Status: "completed"}) {
		t.Fatalf("done = %v, err %v (total %d)", done, err, total)
	}
}

func TestIntegrationCampaignWithNothingSentFails(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	c, _ := approvedCampaign(t, db, "sms")
	if _, err := db.Exec(ctx, "UPDATE messages SET status = 'failed' WHERE campaign_id = $1", c.ID); err != nil {
		t.Fatal(err)
	}
	done, err := NewPGQueue(db).FinishCampaigns(ctx)
	if err != nil || !slices.Contains(done, CampaignDone{ID: c.ID, Status: "failed"}) {
		t.Fatalf("done = %v, err %v", done, err)
	}
}
