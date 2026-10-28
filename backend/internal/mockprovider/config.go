package mockprovider

import (
	"fmt"
	"strconv"
	"time"
)

// Config controls how often and how badly the mock provider misbehaves.
// Rates are probabilities per request in [0, 1].
type Config struct {
	RatePerSec float64 // real per-channel rate limit; 0 disables it
	Burst      int
	RetryAfter time.Duration // sent with every 429

	ThrottleRate     float64 // random 429s on top of the real limit
	ErrorRate        float64 // random 500/503s
	LostResponseRate float64 // message accepted, but the client gets a 500

	MinLatency  time.Duration
	MaxLatency  time.Duration
	SlowRate    float64 // requests that take SlowLatency instead
	SlowLatency time.Duration

	Seed uint64
}

func DefaultConfig() Config {
	return Config{
		RatePerSec:       100,
		Burst:            100,
		RetryAfter:       time.Second,
		ThrottleRate:     0.03,
		ErrorRate:        0.05,
		LostResponseRate: 0.01,
		MinLatency:       20 * time.Millisecond,
		MaxLatency:       150 * time.Millisecond,
		SlowRate:         0.01,
		SlowLatency:      3 * time.Second,
		Seed:             1,
	}
}

// ConfigFromEnv overrides the defaults with MOCK_* environment variables.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	cfg := DefaultConfig()
	p := envParser{getenv: getenv}

	p.float("MOCK_RATE_PER_SEC", &cfg.RatePerSec)
	p.int("MOCK_BURST", &cfg.Burst)
	p.duration("MOCK_RETRY_AFTER", &cfg.RetryAfter)
	p.float("MOCK_THROTTLE_RATE", &cfg.ThrottleRate)
	p.float("MOCK_ERROR_RATE", &cfg.ErrorRate)
	p.float("MOCK_LOST_RESPONSE_RATE", &cfg.LostResponseRate)
	p.duration("MOCK_MIN_LATENCY", &cfg.MinLatency)
	p.duration("MOCK_MAX_LATENCY", &cfg.MaxLatency)
	p.float("MOCK_SLOW_RATE", &cfg.SlowRate)
	p.duration("MOCK_SLOW_LATENCY", &cfg.SlowLatency)
	if p.err != nil {
		return Config{}, p.err
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	for name, r := range map[string]float64{
		"throttle rate": c.ThrottleRate, "error rate": c.ErrorRate,
		"lost response rate": c.LostResponseRate, "slow rate": c.SlowRate,
	} {
		if r < 0 || r > 1 {
			return fmt.Errorf("%s must be in [0, 1], got %v", name, r)
		}
	}
	if sum := c.ThrottleRate + c.ErrorRate + c.LostResponseRate; sum > 1 {
		return fmt.Errorf("throttle, error and lost response rates sum to %v, must be <= 1", sum)
	}
	if c.RatePerSec < 0 || (c.RatePerSec > 0 && c.Burst < 1) {
		return fmt.Errorf("rate limit needs rate >= 0 and burst >= 1 when enabled")
	}
	if c.MinLatency < 0 || c.MaxLatency < c.MinLatency {
		return fmt.Errorf("latency range [%v, %v] is invalid", c.MinLatency, c.MaxLatency)
	}
	return nil
}

type envParser struct {
	getenv func(string) string
	err    error
}

func (p *envParser) parse(key string, fn func(string) error) {
	v := p.getenv(key)
	if v == "" || p.err != nil {
		return
	}
	if err := fn(v); err != nil {
		p.err = fmt.Errorf("%s=%q: %w", key, v, err)
	}
}

func (p *envParser) float(key string, dst *float64) {
	p.parse(key, func(v string) (err error) { *dst, err = strconv.ParseFloat(v, 64); return })
}

func (p *envParser) int(key string, dst *int) {
	p.parse(key, func(v string) (err error) { *dst, err = strconv.Atoi(v); return })
}

func (p *envParser) duration(key string, dst *time.Duration) {
	p.parse(key, func(v string) (err error) { *dst, err = time.ParseDuration(v); return })
}
