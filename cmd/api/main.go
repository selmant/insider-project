package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/insider/insider/internal/api"
	"github.com/insider/insider/internal/api/handler"
	"github.com/insider/insider/internal/app"
	"github.com/insider/insider/internal/observability"
	qredis "github.com/insider/insider/internal/queue/redis"
	"github.com/insider/insider/internal/repository/postgres"
	"github.com/insider/insider/internal/template"
	"github.com/insider/insider/internal/websocket"
)

func main() {
	logger := observability.NewLogger()
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("api server failed", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	infra, err := app.NewInfra(ctx, logger)
	if err != nil {
		return err
	}
	defer infra.Close()

	// Repositories
	notifRepo := postgres.NewNotificationRepo(infra.DB)
	tmplRepo := postgres.NewTemplateRepo(infra.DB)

	// Queue producer (API enqueues, workers consume)
	producer := qredis.NewProducer(infra.Redis)

	// Template engine
	engine := template.NewEngine()

	// WebSocket hub
	hub := websocket.NewHub(logger)

	// Metrics
	metrics := observability.NewMetricsCollector(hub.Len)

	// Handlers
	notifHandler := handler.NewNotificationHandler(notifRepo, tmplRepo, producer, engine, logger)
	tmplHandler := handler.NewTemplateHandler(tmplRepo, logger)
	healthHandler := handler.NewHealthHandler(infra.DB, infra.Redis)
	metricsHandler := handler.NewMetricsHandler(metrics)
	wsHandler := handler.NewWebSocketHandler(hub, logger)

	// Router & server
	cfg := infra.Config
	router := api.NewRouter(logger, notifHandler, tmplHandler, healthHandler, metricsHandler, wsHandler)
	server := api.NewServer(router, cfg.Server.Port, cfg.Server.ReadTimeout, cfg.Server.WriteTimeout, logger)

	go func() {
		if err := server.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server error", "error", err)
			cancel()
		}
	}()

	logger.Info("api server started", "port", cfg.Server.Port)

	<-ctx.Done()
	logger.Info("shutting down api server")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err)
	}

	logger.Info("api server stopped")
	return nil
}
