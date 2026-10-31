// Command worker consumes queued messages and sends them to the provider.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/config"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/delivery"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := store.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	wcfg := delivery.DefaultWorkerConfig()
	worker := delivery.NewWorker(wcfg, delivery.NewPGQueue(pool), delivery.NewClient(cfg.ProviderURL, delivery.DefaultTimeout))
	slog.Info("worker started", "provider_url", cfg.ProviderURL, "rate_per_sec", wcfg.RatePerSec,
		"concurrency", wcfg.Concurrency, "lease", wcfg.Lease)
	worker.Run(ctx)
	slog.Info("worker stopped")
	return nil
}
