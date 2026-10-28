package mockprovider

import (
	"testing"
	"time"
)

func TestConfigFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		check   func(t *testing.T, c Config)
	}{
		{
			name: "defaults",
			check: func(t *testing.T, c Config) {
				if c != DefaultConfig() {
					t.Errorf("got %+v, want defaults", c)
				}
			},
		},
		{
			name: "overrides",
			env:  map[string]string{"MOCK_ERROR_RATE": "0", "MOCK_RATE_PER_SEC": "50", "MOCK_SLOW_LATENCY": "500ms"},
			check: func(t *testing.T, c Config) {
				if c.ErrorRate != 0 || c.RatePerSec != 50 || c.SlowLatency != 500*time.Millisecond {
					t.Errorf("overrides not applied: %+v", c)
				}
			},
		},
		{name: "not a number", env: map[string]string{"MOCK_ERROR_RATE": "lots"}, wantErr: true},
		{name: "rate above 1", env: map[string]string{"MOCK_THROTTLE_RATE": "1.5"}, wantErr: true},
		{name: "rates sum above 1", env: map[string]string{"MOCK_ERROR_RATE": "0.6", "MOCK_THROTTLE_RATE": "0.6"}, wantErr: true},
		{name: "inverted latency", env: map[string]string{"MOCK_MIN_LATENCY": "1s", "MOCK_MAX_LATENCY": "10ms"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ConfigFromEnv(func(k string) string { return tt.env[k] })
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}
