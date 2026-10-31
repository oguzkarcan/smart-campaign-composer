package delivery

import (
	"errors"
	"math/rand/v2"
	"slices"
	"testing"
	"time"
)

func fixed(v float64) func() float64 { return func() float64 { return v } }

func TestBackoff(t *testing.T) {
	// Without jitter the full delay doubles from 1 s and stops at 60 s.
	full := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60, 60}
	for i, want := range full {
		attempt := i + 1
		want *= time.Second
		if got := Backoff(attempt, fixed(0)); got != want/2 {
			t.Errorf("attempt %d, no jitter: %v, want %v", attempt, got, want/2)
		}
		if got := Backoff(attempt, fixed(0.999999)); got < want-time.Millisecond || got > want {
			t.Errorf("attempt %d, max jitter: %v, want just under %v", attempt, got, want)
		}
	}
	// Large attempt numbers must not overflow.
	if got := Backoff(1000, fixed(0.5)); got != 45*time.Second {
		t.Errorf("attempt 1000: %v", got)
	}
}

func TestDecide(t *testing.T) {
	retry := Result{Outcome: Retry, StatusCode: 503, Err: errors.New("down")}
	tooMany := Result{Outcome: Retry, StatusCode: 429, RetryAfter: 10 * time.Second, Err: errors.New("slow down")}
	short := Result{Outcome: Retry, StatusCode: 429, RetryAfter: time.Second, Err: errors.New("slow down")}
	huge := Result{Outcome: Retry, StatusCode: 429, RetryAfter: time.Hour, Err: errors.New("slow down")}
	tests := []struct {
		name    string
		res     Result
		attempt int
		want    Decision
	}{
		{"sent", Result{Outcome: Sent}, 1, Decision{Status: StatusSent}},
		{"sent on the last attempt", Result{Outcome: Sent}, MaxAttempts, Decision{Status: StatusSent}},
		{"permanent fails at once", Result{Outcome: Permanent, StatusCode: 400}, 1, Decision{Status: StatusFailed}},
		{"retryable waits the backoff", retry, 3, Decision{Status: StatusRetry, Delay: 2 * time.Second}},
		{"Retry-After longer than the backoff wins", tooMany, 1, Decision{Status: StatusRetry, Delay: 10 * time.Second}},
		{"backoff longer than Retry-After wins", short, 4, Decision{Status: StatusRetry, Delay: 4 * time.Second}},
		{"Retry-After is capped", huge, 1, Decision{Status: StatusRetry, Delay: MaxRetryAfter}},
		{"last attempt fails", retry, MaxAttempts, Decision{Status: StatusFailed}},
	}
	for _, tt := range tests {
		if got := decide(tt.res, tt.attempt, fixed(0)); got != tt.want {
			t.Errorf("%s: %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

// A message that only ever gets retryable errors is tried exactly
// MaxAttempts times with growing waits, then fails; one that gets a 429 on
// its last attempt fails too, and a permanent error ignores Retry-After.
func TestRetryPolicySequence(t *testing.T) {
	down := Result{Outcome: Retry, StatusCode: 500}
	var waits []time.Duration
	attempt := 1
	for ; ; attempt++ {
		d := decide(down, attempt, fixed(0))
		if d.Status != StatusRetry {
			if d.Status != StatusFailed {
				t.Fatalf("attempt %d: status %s", attempt, d.Status)
			}
			break
		}
		waits = append(waits, d.Delay)
	}
	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second}
	if attempt != MaxAttempts || !slices.Equal(waits, want) {
		t.Fatalf("failed after %d attempts with waits %v, want %d attempts and %v", attempt, waits, MaxAttempts, want)
	}

	tooMany := Result{Outcome: Retry, StatusCode: 429, RetryAfter: time.Second}
	if d := decide(tooMany, MaxAttempts, fixed(0)); d.Status != StatusFailed {
		t.Errorf("429 on the last attempt: %+v, want failed", d)
	}
	rejected := Result{Outcome: Permanent, StatusCode: 400, RetryAfter: time.Second}
	if d := decide(rejected, 1, fixed(0)); d != (Decision{Status: StatusFailed}) {
		t.Errorf("permanent error with Retry-After: %+v, want failed at once", d)
	}
}

// Out-of-range attempt numbers still give a positive wait, never zero or
// negative, so a bad counter cannot cause a hot retry loop.
func TestBackoffNeverZero(t *testing.T) {
	for _, attempt := range []int{-3, 0} {
		if got := Backoff(attempt, fixed(0)); got != BaseDelay/2 {
			t.Errorf("attempt %d: %v, want %v", attempt, got, BaseDelay/2)
		}
	}
}

// With real random draws every wait stays within [half, full) of the
// exponential delay, so waits never shrink as attempts go up.
func TestBackoffJitterBounds(t *testing.T) {
	rnd := rand.New(rand.NewPCG(1, 2))
	for attempt := 1; attempt <= 10; attempt++ {
		full := min(BaseDelay<<(attempt-1), MaxDelay)
		lo, hi := full, time.Duration(0)
		for range 1000 {
			d := Backoff(attempt, rnd.Float64)
			if d < full/2 || d >= full {
				t.Fatalf("attempt %d: %v outside [%v, %v)", attempt, d, full/2, full)
			}
			lo, hi = min(lo, d), max(hi, d)
		}
		// The draws actually spread over the range (jitter is not constant).
		if hi-lo < full/4 {
			t.Errorf("attempt %d: draws only span %v-%v", attempt, lo, hi)
		}
	}
}
