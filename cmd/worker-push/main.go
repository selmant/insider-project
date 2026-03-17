package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/insider/insider/internal/app"
	"github.com/insider/insider/internal/domain"
	"github.com/insider/insider/internal/observability"
)

func main() {
	logger := observability.NewLogger().With("service", "worker-push")
	slog.SetDefault(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	infra, err := app.NewInfra(ctx, logger)
	if err != nil {
		logger.Error("failed to initialize", "error", err)
		os.Exit(1)
	}
	defer infra.Close()

	if err := app.RunWorker(ctx, infra, domain.ChannelPush, logger); err != nil {
		logger.Error("worker failed", "error", err)
		os.Exit(1)
	}
}
