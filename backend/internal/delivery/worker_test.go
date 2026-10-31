package delivery

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// memQueue is an in-memory Queue. Retries are due again at once.
type memQueue struct {
	mu       sync.Mutex
	due      []Job
	attempts map[int64]int
	status   map[int64]string
	finished []Job
	released []Job
	claimed  int
}

func newMemQueue(channel string, n int) *memQueue {
	q := &memQueue{attempts: map[int64]int{}, status: map[int64]string{}}
	for i := range n {
		id := int64(i + 1)
		q.due = append(q.due, Job{MessageID: id, Channel: channel, Phone: "+905321234567", Body: "Hi"})
		q.status[id] = StatusQueued
	}
	return q
}

func (q *memQueue) Claim(ctx context.Context, channel string, n int, _ time.Duration) ([]Job, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	n = min(n, len(q.due))
	jobs := slices.Clone(q.due[:n])
	q.due = q.due[n:]
	for i := range jobs {
		q.attempts[jobs[i].MessageID]++
		jobs[i].Attempt = q.attempts[jobs[i].MessageID]
		q.status[jobs[i].MessageID] = StatusProcessing
	}
	q.claimed += len(jobs)
	return jobs, nil
}

func (q *memQueue) Finish(_ context.Context, j Job, _ Result, d Decision) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.finished = append(q.finished, j)
	q.status[j.MessageID] = d.Status
	if d.Status == StatusRetry {
		q.due = append(q.due, j)
	}
	return true, nil
}

func (q *memQueue) Release(_ context.Context, jobs []Job) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, j := range jobs {
		q.attempts[j.MessageID]--
		q.status[j.MessageID] = StatusQueued
	}
	q.released = append(q.released, jobs...)
	return nil
}

func (q *memQueue) RecoverLeases(context.Context) (int, error)              { return 0, nil }
func (q *memQueue) FinishCampaigns(context.Context) ([]CampaignDone, error) { return nil, nil }

func (q *memQueue) count(status string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, s := range q.status {
		if s == status {
			n++
		}
	}
	return n
}

// fakeSender answers with results from a function and counts calls.
type fakeSender struct {
	calls  atomic.Int64
	answer func(j Job) Result
}

func (s *fakeSender) Send(_ context.Context, j Job) Result {
	s.calls.Add(1)
	return s.answer(j)
}

func testConfig() WorkerConfig {
	return WorkerConfig{
		Channels: []string{"sms"}, RatePerSec: 10000, Burst: 1, Concurrency: 4,
		Lease: 30 * time.Second, PollInterval: time.Millisecond, HousekeepingInterval: time.Millisecond,
	}
}

// runUntil runs the worker until cond holds (or a timeout) and returns once
// it has shut down.
func runUntil(t *testing.T, w *Worker, cond func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down")
	}
	if !cond() {
		t.Fatal("condition not reached")
	}
}

func TestWorkerSendsEverythingOnce(t *testing.T) {
	q := newMemQueue("sms", 50)
	s := &fakeSender{answer: func(Job) Result { return Result{Outcome: Sent} }}
	w := NewWorker(testConfig(), q, s)
	runUntil(t, w, func() bool { return q.count(StatusSent) == 50 })
	if s.calls.Load() != 50 || len(q.finished) != 50 {
		t.Fatalf("calls %d, finished %d", s.calls.Load(), len(q.finished))
	}
}

func TestWorkerRetriesThenFails(t *testing.T) {
	q := newMemQueue("sms", 3)
	s := &fakeSender{answer: func(j Job) Result {
		switch {
		case j.MessageID == 1 && j.Attempt < 3: // recovers on the third attempt
			return Result{Outcome: Retry, StatusCode: 503}
		case j.MessageID == 2: // never recovers
			return Result{Outcome: Retry, StatusCode: 500}
		case j.MessageID == 3: // permanent
			return Result{Outcome: Permanent, StatusCode: 400}
		}
		return Result{Outcome: Sent}
	}}
	w := NewWorker(testConfig(), q, s)
	w.rnd = fixed(0)
	runUntil(t, w, func() bool { return q.count(StatusSent)+q.count(StatusFailed) == 3 })

	if q.status[1] != StatusSent || q.attempts[1] != 3 {
		t.Errorf("message 1: %s after %d attempts", q.status[1], q.attempts[1])
	}
	if q.status[2] != StatusFailed || q.attempts[2] != MaxAttempts {
		t.Errorf("message 2: %s after %d attempts, want failed after %d", q.status[2], q.attempts[2], MaxAttempts)
	}
	if q.status[3] != StatusFailed || q.attempts[3] != 1 {
		t.Errorf("message 3: %s after %d attempts, want failed after 1", q.status[3], q.attempts[3])
	}
}

func TestWorkerRateLimit(t *testing.T) {
	const rate, n = 200.0, 60
	q := newMemQueue("sms", n)
	s := &fakeSender{answer: func(Job) Result { return Result{Outcome: Sent} }}
	cfg := testConfig()
	cfg.RatePerSec, cfg.Burst, cfg.Concurrency = rate, 1, 16
	start := time.Now()
	runUntil(t, NewWorker(cfg, q, s), func() bool { return q.count(StatusSent) == n })
	// One token up front, then one every 5 ms: at least (n-1)/rate.
	if elapsed, min := time.Since(start), time.Duration(float64(n-1)/rate*float64(time.Second)); elapsed < min {
		t.Fatalf("%d messages at %v/s took %v, want at least %v", n, rate, elapsed, min)
	}
}

// On shutdown, calls in flight finish and are recorded, claimed messages
// that were not sent go back to the queue, and nothing is lost.
func TestWorkerGracefulShutdown(t *testing.T) {
	q := newMemQueue("sms", 20)
	started := make(chan struct{}, 20)
	s := &fakeSender{answer: func(Job) Result {
		started <- struct{}{}
		time.Sleep(50 * time.Millisecond)
		return Result{Outcome: Sent}
	}}
	cfg := testConfig()
	cfg.RatePerSec, cfg.Burst, cfg.Concurrency = 20, 1, 8 // slow tokens: claimed jobs wait
	w := NewWorker(cfg, q, s)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	<-started
	cancel()
	<-done

	calls := int(s.calls.Load())
	if len(q.finished) != calls {
		t.Fatalf("%d calls but %d finished: an in-flight call was dropped", calls, len(q.finished))
	}
	if len(q.released) == 0 || q.claimed != calls+len(q.released) {
		t.Fatalf("claimed %d, sent %d, released %d", q.claimed, calls, len(q.released))
	}
	if got := q.count(StatusProcessing); got != 0 {
		t.Fatalf("%d messages left processing", got)
	}
	for _, j := range q.released {
		if q.attempts[j.MessageID] != 0 {
			t.Fatalf("released message %d kept its attempt", j.MessageID)
		}
	}
}
