package delivery

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/content"
)

// Queue is the message queue the worker consumes; PGQueue implements it.
type Queue interface {
	Claim(ctx context.Context, channel string, n int, lease time.Duration) ([]Job, error)
	Finish(ctx context.Context, j Job, res Result, d Decision) (bool, error)
	Release(ctx context.Context, jobs []Job) error
	RecoverLeases(ctx context.Context) (int, error)
	StartScheduled(ctx context.Context) ([]int64, error)
	FinishCampaigns(ctx context.Context) ([]CampaignDone, error)
}

// Sender sends one message; Client implements it.
type Sender interface {
	Send(ctx context.Context, j Job) Result
}

type WorkerConfig struct {
	Channels []string
	// RatePerSec and Burst are the token bucket per channel.
	RatePerSec float64
	Burst      int
	// Concurrency is how many provider calls may be in flight per channel.
	Concurrency int
	// Lease is how long a claimed message belongs to this worker.
	Lease time.Duration
	// PollInterval is the wait when nothing is due.
	PollInterval time.Duration
	// HousekeepingInterval paces lease recovery, starting scheduled
	// campaigns and campaign completion.
	HousekeepingInterval time.Duration
}

// DefaultWorkerConfig sends at most 100 messages per second per channel.
// The burst is small so the provider's own 100/s limit is not hit at the
// edges of a second; 32 calls in flight cover 100/s at the provider's
// typical latency with room for slow responses.
func DefaultWorkerConfig() WorkerConfig {
	return WorkerConfig{
		Channels:             []string{content.ChannelSMS, content.ChannelEmail},
		RatePerSec:           100,
		Burst:                10,
		Concurrency:          32,
		Lease:                30 * time.Second,
		PollInterval:         250 * time.Millisecond,
		HousekeepingInterval: time.Second,
	}
}

type Worker struct {
	cfg    WorkerConfig
	queue  Queue
	sender Sender
	rnd    func() float64
}

func NewWorker(cfg WorkerConfig, queue Queue, sender Sender) *Worker {
	return &Worker{cfg: cfg, queue: queue, sender: sender, rnd: rand.Float64}
}

// Run sends until ctx is cancelled, then shuts down gracefully: it stops
// claiming, returns claimed but unsent messages to the queue, and waits for
// calls already in flight to finish and be recorded (each bounded by the
// client's timeout). Whatever is still left is recovered by the lease.
func (w *Worker) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, ch := range w.cfg.Channels {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.runChannel(ctx, ch)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.housekeeping(ctx)
	}()
	wg.Wait()
}

// runChannel claims only as many messages as it has free send slots, so a
// claimed message waits at most Concurrency/RatePerSec for a token, far
// below the lease.
func (w *Worker) runChannel(ctx context.Context, channel string) {
	limiter := rate.NewLimiter(rate.Limit(w.cfg.RatePerSec), w.cfg.Burst)
	slots := make(chan struct{}, w.cfg.Concurrency)
	var inflight sync.WaitGroup
	defer inflight.Wait()

	for ctx.Err() == nil {
		// Block for one free slot, then take any others that are free.
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		n := 1
	more:
		for n < w.cfg.Concurrency {
			select {
			case slots <- struct{}{}:
				n++
			default:
				break more
			}
		}

		jobs, err := w.queue.Claim(ctx, channel, n, w.cfg.Lease)
		if err != nil && ctx.Err() == nil {
			slog.Error("claim failed", "channel", channel, "err", err)
		}
		for range n - len(jobs) {
			<-slots
		}
		if len(jobs) == 0 {
			sleep(ctx, w.cfg.PollInterval)
			continue
		}

		for i, j := range jobs {
			if err := limiter.Wait(ctx); err != nil {
				w.release(jobs[i:])
				for range len(jobs) - i {
					<-slots
				}
				return
			}
			inflight.Add(1)
			go func() {
				defer inflight.Done()
				defer func() { <-slots }()
				w.send(ctx, j)
			}()
		}
	}
}

// send makes one provider call and records it. A call that has started is
// finished even during shutdown, so its outcome is not lost: cancelling it
// would leave the provider's answer unknown and cost an attempt.
func (w *Worker) send(ctx context.Context, j Job) {
	ctx = context.WithoutCancel(ctx)
	res := w.sender.Send(ctx, j)
	d := decide(res, j.Attempt, w.rnd)
	updated, err := w.queue.Finish(ctx, j, res, d)
	switch {
	case err != nil:
		// The lease will put the message back in the queue.
		slog.Error("finish message failed", "message_id", j.MessageID, "err", err)
	case !updated:
		slog.Warn("message was reclaimed before its attempt finished", "message_id", j.MessageID, "attempt", j.Attempt)
	case d.Status != StatusSent:
		slog.Info("message not sent", "message_id", j.MessageID, "channel", j.Channel, "attempt", j.Attempt,
			"status", d.Status, "http_status", res.StatusCode, "retry_in", d.Delay, "err", res.Err)
	}
}

func (w *Worker) release(jobs []Job) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := w.queue.Release(ctx, jobs); err != nil {
		slog.Error("release messages failed; the lease will recover them", "count", len(jobs), "err", err)
	}
}

// housekeeping recovers expired leases, starts scheduled campaigns that are
// due and closes finished campaigns. Starting comes before closing, so a
// campaign whose messages all went out in the moment between its time and
// its start is closed in the same round.
func (w *Worker) housekeeping(ctx context.Context) {
	t := time.NewTicker(w.cfg.HousekeepingInterval)
	defer t.Stop()
	for {
		if n, err := w.queue.RecoverLeases(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("lease recovery failed", "err", err)
		} else if n > 0 {
			slog.Warn("recovered messages with expired leases", "count", n)
		}
		started, err := w.queue.StartScheduled(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("starting scheduled campaigns failed", "err", err)
		}
		for _, id := range started {
			slog.Info("scheduled campaign started sending", "campaign_id", id)
		}
		done, err := w.queue.FinishCampaigns(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("campaign completion failed", "err", err)
		}
		for _, c := range done {
			slog.Info("campaign finished", "campaign_id", c.ID, "status", c.Status)
		}
		select {
		case <-t.C:
		case <-ctx.Done():
			return
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}
