// Command api serves the HTTP API and SSE streams.
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

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/campaign"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/config"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/httpapi"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/llm"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/metrics"
	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("api failed", "err", err)
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

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.New(pool, campaign.NewService(campaign.NewPGStore(pool), newLLM(cfg)), metrics.NewPG(pool)).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	model := "mock"
	if cfg.LLMMode == config.LLMModeClaude {
		model = cfg.ClaudeModel
	}
	slog.Info("api listening", "addr", cfg.HTTPAddr, "llm_mode", cfg.LLMMode, "model", model)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// mockStreamDelay paces the mock's streamed chunks so streaming is visible in
// a demo (about a second for a full SMS and email).
const mockStreamDelay = 40 * time.Millisecond

func newLLM(cfg config.Config) llm.Client {
	if cfg.LLMMode == config.LLMModeClaude {
		return llm.NewClaude(cfg.AnthropicAPIKey, cfg.ClaudeModel)
	}
	return &llm.Mock{Delay: mockStreamDelay}
}
