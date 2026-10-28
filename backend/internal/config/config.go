// Package config loads service configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
)

const (
	LLMModeMock   = "mock"
	LLMModeClaude = "claude"
)

type Config struct {
	DatabaseURL     string
	HTTPAddr        string
	ProviderURL     string
	LLMMode         string
	ClaudeModel     string
	AnthropicAPIKey string
}

// Load reads the configuration from the environment, applying defaults, and
// validates it.
func Load() (Config, error) {
	cfg := Config{
		DatabaseURL:     getenv("DATABASE_URL", "postgres://scc:scc@localhost:5432/scc?sslmode=disable"),
		HTTPAddr:        getenv("HTTP_ADDR", ":8080"),
		ProviderURL:     getenv("PROVIDER_URL", "http://localhost:9090"),
		LLMMode:         getenv("LLM_MODE", LLMModeMock),
		ClaudeModel:     getenv("CLAUDE_MODEL", "claude-sonnet-5-5"),
		AnthropicAPIKey: os.Getenv("ANTHROPIC_API_KEY"),
	}
	return cfg, cfg.validate()
}

func (c Config) validate() error {
	switch c.LLMMode {
	case LLMModeMock:
	case LLMModeClaude:
		if c.AnthropicAPIKey == "" {
			return errors.New("ANTHROPIC_API_KEY is required when LLM_MODE=claude")
		}
	default:
		return fmt.Errorf("LLM_MODE must be %q or %q, got %q", LLMModeMock, LLMModeClaude, c.LLMMode)
	}
	return nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
