package config

import "testing"

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		check   func(t *testing.T, c Config)
	}{
		{
			name: "defaults to mock LLM without a key",
			check: func(t *testing.T, c Config) {
				if c.LLMMode != LLMModeMock {
					t.Errorf("LLMMode = %q, want %q", c.LLMMode, LLMModeMock)
				}
				if c.ClaudeModel != "claude-sonnet-5-5" {
					t.Errorf("ClaudeModel = %q, want default", c.ClaudeModel)
				}
			},
		},
		{
			name:    "claude mode requires an API key",
			env:     map[string]string{"LLM_MODE": "claude"},
			wantErr: true,
		},
		{
			name: "claude mode with key and model override",
			env: map[string]string{
				"LLM_MODE":          "claude",
				"ANTHROPIC_API_KEY": "test-key",
				"CLAUDE_MODEL":      "claude-opus-5-5",
			},
			check: func(t *testing.T, c Config) {
				if c.ClaudeModel != "claude-opus-5-5" {
					t.Errorf("ClaudeModel = %q, want override", c.ClaudeModel)
				}
			},
		},
		{
			name:    "unknown LLM mode is rejected",
			env:     map[string]string{"LLM_MODE": "gpt"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"LLM_MODE", "ANTHROPIC_API_KEY", "CLAUDE_MODEL"} {
				t.Setenv(k, "")
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			cfg, err := Load()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}
