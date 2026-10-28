// Command seed fills the database with fake users and events. It does nothing
// if users already exist.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/config"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/seed"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("seed failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	users, err := envInt("SEED_USERS", 5000)
	if err != nil {
		return err
	}
	rngSeed, err := envInt("SEED", 42)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := store.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	now := time.Now().UTC()
	data := seed.Generate(seed.Config{Users: users, Seed: uint64(rngSeed), Now: now})

	inserted, err := seed.Load(ctx, pool, data)
	if err != nil {
		return err
	}
	if !inserted {
		slog.Info("seed: users already exist, skipping")
		return nil
	}

	s := seed.Summarize(data, now)
	slog.Info("seed: done",
		"users", s.Users, "events", s.Events,
		"languages", s.ByLanguage, "consent", s.ByConsent, "event_types", s.ByEventType,
		"example_brief_audience", s.ExampleAudience)
	return nil
}

func envInt(key string, fallback int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer, got %q", key, v)
	}
	return n, nil
}
