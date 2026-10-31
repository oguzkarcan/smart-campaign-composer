package delivery

import "time"

const (
	// MaxAttempts is how many provider calls a message gets in total.
	MaxAttempts = 5
	// BaseDelay is the wait after the first failed attempt; it doubles with
	// every attempt up to MaxDelay.
	BaseDelay = time.Second
	MaxDelay  = 60 * time.Second
	// MaxRetryAfter bounds how long a provider's Retry-After can park a
	// message, so a broken header cannot stall a campaign for hours.
	MaxRetryAfter = 5 * time.Minute
)

// Message statuses written by the worker.
const (
	StatusQueued     = "queued"
	StatusProcessing = "processing"
	StatusSent       = "sent"
	StatusRetry      = "retry"
	StatusFailed     = "failed"
)

// Backoff is the wait after failed attempt number attempt (1-based):
// exponential (1 s, 2 s, 4 s, ... capped at 60 s) with "equal jitter", a
// random value between half and all of it. Jitter spreads out messages that
// failed together (e.g. a provider outage) so they do not retry in lockstep;
// keeping the lower half means the wait still grows with every attempt.
// rnd returns a value in [0, 1).
func Backoff(attempt int, rnd func() float64) time.Duration {
	d := MaxDelay
	// 1 s << 6 is already past the cap; checking first avoids overflow.
	if attempt < 7 {
		d = min(BaseDelay<<max(attempt-1, 0), MaxDelay)
	}
	half := d / 2
	return half + time.Duration(rnd()*float64(d-half))
}

// Decision is what happens to a message after an attempt.
type Decision struct {
	Status string        // StatusSent, StatusRetry or StatusFailed
	Delay  time.Duration // for StatusRetry: wait before the next attempt
}

// decide applies the retry policy to the result of attempt number attempt:
// permanent errors fail at once, retryable ones wait the backoff or the
// provider's Retry-After, whichever is longer, until MaxAttempts is reached.
func decide(res Result, attempt int, rnd func() float64) Decision {
	switch {
	case res.Outcome == Sent:
		return Decision{Status: StatusSent}
	case res.Outcome == Permanent, attempt >= MaxAttempts:
		return Decision{Status: StatusFailed}
	}
	return Decision{Status: StatusRetry, Delay: max(Backoff(attempt, rnd), min(res.RetryAfter, MaxRetryAfter))}
}
