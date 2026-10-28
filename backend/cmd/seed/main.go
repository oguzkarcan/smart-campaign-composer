// Command seed fills the database with fake users and events.
package main

import (
	"log/slog"
	"os"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/config"
)

func main() {
	if _, err := config.Load(); err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}
	slog.Info("seed: nothing to do yet")
}
