//go:build integration

package delivery

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/campaign"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/mockprovider"
)

// TestIntegrationEndToEnd runs the real worker (Postgres queue, HTTP client,
// rate limiter, retries) against the mock provider with its own rate limit
// and injected 429s, 5xx and lost responses, plus one recipient the provider
// rejects permanently. It checks the guarantees, not a lucky run:
//
//   - every message ends sent or failed and the campaign completes;
//   - nobody receives the same message twice (duplicate counter stays 0);
//   - every provider call is recorded in message_attempts;
//   - a permanent error fails at once, retries stop at MaxAttempts;
//   - Retry-After is honoured before the next attempt;
//   - the worker's limiter keeps us under the provider's 100/s.
func TestIntegrationEndToEnd(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	c, total := approvedCampaign(t, db, "sms", "email")

	// The recipient of one SMS is rejected with a 400, as a provider would
	// reject an unreachable number.
	var rejectPhone string
	var rejectedID int64
	err := db.QueryRow(ctx, `SELECT m.id, u.phone FROM messages m JOIN users u ON u.id = m.user_id
		WHERE m.campaign_id = $1 AND m.channel = 'sms' ORDER BY m.id LIMIT 1`, c.ID).Scan(&rejectedID, &rejectPhone)
	if err != nil {
		t.Fatal(err)
	}

	pcfg := mockprovider.DefaultConfig()
	pcfg.ThrottleRate, pcfg.ErrorRate, pcfg.LostResponseRate = 0.05, 0.08, 0.03
	pcfg.RetryAfter = time.Second
	pcfg.SlowRate = 0 // no 3 s responses: keeps the test fast and timeouts out of the call count
	provider := mockprovider.New(pcfg)
	var rejected atomic.Int64
	srv := httptest.NewServer(rejectRecipient(provider.Handler(), rejectPhone, &rejected))
	defer srv.Close()

	wcfg := DefaultWorkerConfig()
	wcfg.PollInterval, wcfg.HousekeepingInterval = 20*time.Millisecond, 100*time.Millisecond
	worker := NewWorker(wcfg, NewPGQueue(db), NewClient(srv.URL, DefaultTimeout))
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { worker.Run(runCtx); close(done) }()

	svc := campaign.NewService(campaign.NewPGStore(db), nil)
	deadline := time.Now().Add(90 * time.Second)
	var p campaign.Progress
	for {
		if p, err = svc.Progress(ctx, c.ID); err != nil {
			t.Fatal(err)
		}
		if p.Done || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	stop()
	<-done
	if p.Status != campaign.StatusCompleted {
		t.Fatalf("campaign %s after the deadline: %+v", p.Status, p.Totals)
	}

	// Every message reached a final state.
	if p.Totals.Total != total || p.Totals.Sent+p.Totals.Failed != total {
		t.Fatalf("totals = %+v, want %d sent or failed", p.Totals, total)
	}

	// Nobody got a message twice, and the provider accepted each sent
	// message exactly once (a lost response is accepted, then replayed).
	stats := provider.Stats()
	var calls, accepted int64
	for ch, st := range stats {
		if st.DuplicateDeliveries != 0 || st.Conflicts != 0 {
			t.Errorf("%s: %d duplicate deliveries, %d key conflicts", ch, st.DuplicateDeliveries, st.Conflicts)
		}
		if st.RateLimited != 0 {
			t.Errorf("%s: the provider's own rate limit was hit %d times; the worker's limiter should prevent it", ch, st.RateLimited)
		}
		calls += st.Accepted + st.Replayed + st.RateLimited + st.InjectedThrottles + st.InjectedErrors + st.Rejected + st.Conflicts
		accepted += st.Accepted
	}
	t.Logf("provider: %+v; rejected by the test: %d", stats, rejected.Load())
	if accepted < int64(p.Totals.Sent) || accepted > int64(p.Totals.Sent+p.Totals.Failed) {
		t.Errorf("provider accepted %d, sent %d, failed %d", accepted, p.Totals.Sent, p.Totals.Failed)
	}
	if stats["sms"].Replayed+stats["email"].Replayed == 0 && stats["sms"].LostResponses+stats["email"].LostResponses > 0 {
		t.Error("lost responses were never replayed")
	}

	// Every provider call is in message_attempts, with its outcome.
	var recorded, unclassified int64
	err = db.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE a.outcome IS NULL)
		FROM message_attempts a JOIN messages m ON m.id = a.message_id WHERE m.campaign_id = $1`, c.ID).Scan(&recorded, &unclassified)
	if err != nil {
		t.Fatal(err)
	}
	if recorded != calls+rejected.Load() || unclassified != 0 {
		t.Errorf("recorded %d attempts (%d unclassified), provider saw %d calls + %d rejected", recorded, unclassified, calls, rejected.Load())
	}

	// The rejected recipient failed on its first attempt, permanently.
	var status string
	var attempts int
	var outcome string
	err = db.QueryRow(ctx, `SELECT m.status, m.attempts, a.outcome FROM messages m
		JOIN message_attempts a ON a.message_id = m.id WHERE m.id = $1`, rejectedID).Scan(&status, &attempts, &outcome)
	if err != nil || status != StatusFailed || attempts != 1 || outcome != "permanent" {
		t.Errorf("rejected message: %s after %d attempts (%s), err %v", status, attempts, outcome, err)
	}
	if len(p.Failures) == 0 || !strings.Contains(p.Failures[0].Error, "400") {
		t.Errorf("failure reasons = %+v", p.Failures)
	}

	// Any other failure used up every attempt on retryable errors.
	var badFailures int
	err = db.QueryRow(ctx, `SELECT count(*) FROM messages m WHERE m.campaign_id = $1 AND m.status = 'failed' AND m.id <> $2
		AND (m.attempts <> $3 OR EXISTS (SELECT 1 FROM message_attempts a WHERE a.message_id = m.id AND a.outcome <> 'retry'))`,
		c.ID, rejectedID, MaxAttempts).Scan(&badFailures)
	if err != nil || badFailures != 0 {
		t.Errorf("%d messages failed without exhausting their retries (err %v)", badFailures, err)
	}
	var overMax int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM messages WHERE campaign_id = $1 AND attempts > $2", c.ID, MaxAttempts).Scan(&overMax); err != nil || overMax != 0 {
		t.Errorf("%d messages exceeded %d attempts (err %v)", overMax, MaxAttempts, err)
	}

	// After a 429 the next attempt came at least Retry-After later. Attempt
	// rows are written when a call finishes; the next call starts no earlier
	// than next_attempt_at = finish + delay, so its row is later still.
	var early int
	err = db.QueryRow(ctx, `
		SELECT count(*) FROM message_attempts a
		JOIN message_attempts b ON b.message_id = a.message_id AND b.attempt = a.attempt + 1
		JOIN messages m ON m.id = a.message_id
		WHERE m.campaign_id = $1 AND a.http_status = 429 AND b.created_at - a.created_at < $2::interval`,
		c.ID, pcfg.RetryAfter.String()).Scan(&early)
	if err != nil || early != 0 {
		t.Errorf("%d retries came before Retry-After (err %v)", early, err)
	}

	// No second had more calls per channel than the limiter allows. Rows are
	// stamped when a call finishes, so a second can also hold calls started
	// up to the provider's maximum latency before it.
	var busiest int
	err = db.QueryRow(ctx, `
		SELECT coalesce(max(n), 0) FROM (
			SELECT count(*) AS n FROM message_attempts a JOIN messages m ON m.id = a.message_id
			WHERE m.campaign_id = $1 GROUP BY m.channel, date_trunc('second', a.created_at)) per_second`, c.ID).Scan(&busiest)
	if limit := int(wcfg.RatePerSec*(1+pcfg.MaxLatency.Seconds())) + wcfg.Burst; err != nil || busiest > limit {
		t.Errorf("busiest second had %d calls on one channel, limit %d (err %v)", busiest, limit, err)
	}
}

// rejectRecipient answers 400 for messages to phone, like a provider
// rejecting an unreachable number, and passes everything else through.
func rejectRecipient(next http.Handler, phone string, count *atomic.Int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req SendRequest
		if json.Unmarshal(body, &req) == nil && req.To == phone {
			count.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error": "to: number is not reachable"}`))
			return
		}
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		next.ServeHTTP(w, r)
	})
}
