// Command worker consumes queued messages and sends them to the provider.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("worker started", "provider_url", cfg.ProviderURL)
	<-ctx.Done()
	slog.Info("worker stopped")
}
