package delivery

import (
	"errors"
	"math/rand/v2"
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
