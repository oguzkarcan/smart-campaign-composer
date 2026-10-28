// Command mockprovider imitates an SMS/email provider, including failures,
// latency and 429 responses. Nothing is ever delivered.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/mockprovider"
)

func main() {
	addr := os.Getenv("MOCK_PROVIDER_ADDR")
	if addr == "" {
		addr = ":9090"
	}
	cfg, err := mockprovider.ConfigFromEnv(os.Getenv)
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{
		Addr:              addr,
		Handler:           mockprovider.New(cfg).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	slog.Info("mock provider listening", "addr", addr,
		"rate_per_sec", cfg.RatePerSec, "throttle_rate", cfg.ThrottleRate,
		"error_rate", cfg.ErrorRate, "lost_response_rate", cfg.LostResponseRate,
		"latency", cfg.MinLatency.String()+"-"+cfg.MaxLatency.String(),
		"slow_rate", cfg.SlowRate, "slow_latency", cfg.SlowLatency)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("mock provider server", "err", err)
		os.Exit(1)
	}
}
