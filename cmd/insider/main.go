package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"

	"github.com/insider/insider/internal/api"
	"github.com/insider/insider/internal/api/handler"
	"github.com/insider/insider/internal/config"
	"github.com/insider/insider/internal/migration"
	"github.com/insider/insider/internal/observability"
	"github.com/insider/insider/internal/provider"
	"github.com/insider/insider/internal/provider/webhook"
	qredis "github.com/insider/insider/internal/queue/redis"
	"github.com/insider/insider/internal/repository/postgres"
	"github.com/insider/insider/internal/retry"
	"github.com/insider/insider/internal/template"
	"github.com/insider/insider/internal/websocket"
	"github.com/insider/insider/internal/worker"
)

func main() {
	logger := observability.NewLogger()
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("application failed", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Connect to PostgreSQL
	pool, err := pgxpool.New(ctx, cfg.Database.DSN())
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}
	logger.Info("connected to PostgreSQL")

	// Run migrations
	sqlDB, err := sql.Open("pgx", cfg.Database.DSN())
	if err != nil {
		return fmt.Errorf("open sql db for migrations: %w", err)
	}
	if err := migration.Run(sqlDB); err != nil {
		sqlDB.Close()
		return fmt.Errorf("run migrations: %w", err)
	}
	sqlDB.Close()
	logger.Info("migrations applied")

	// Connect to Redis
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	defer rdb.Close()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("connect to redis: %w", err)
	}
	logger.Info("connected to Redis")

	// Initialize tracing
	if cfg.Tracing.Enabled {
		shutdown, err := observability.InitTracing(ctx, cfg.Tracing.Endpoint)
		if err != nil {
			logger.Warn("tracing init failed, continuing without tracing", "error", err)
		} else {
			defer shutdown(ctx)
			logger.Info("tracing enabled")
		}
	}

	// Repositories
	notifRepo := postgres.NewNotificationRepo(pool)
	tmplRepo := postgres.NewTemplateRepo(pool)

	// Queue
	producer := qredis.NewProducer(rdb)
	consumer := qredis.NewConsumer(rdb)
	rateLimiter := qredis.NewRateLimiter(rdb, cfg.Worker.RateLimit)

	// Template engine
	engine := template.NewEngine()

	// WebSocket hub
	hub := websocket.NewHub(logger)

	// Metrics
	metrics := observability.NewMetricsCollector(hub.Len)

	// Provider with circuit breaker
	webhookClient := webhook.NewClient(cfg.Provider.WebhookURL, cfg.Provider.RequestTimeout)
	var prov provider.Provider = provider.NewCircuitBreaker(
		webhookClient,
		cfg.Provider.CBMaxFailures,
		cfg.Provider.CBTimeout,
	)

	// Retry strategy
	retrier := retry.NewStrategy(cfg.Worker.RetryBaseWait, cfg.Worker.RetryMaxWait)
	deadLetter := retry.NewDeadLetterHandler(pool)

	// Worker processor & dispatcher
	processor := worker.NewProcessor(notifRepo, prov, retrier, deadLetter, hub, metrics, logger)
	dispatcher := worker.NewDispatcher(consumer, rateLimiter, processor, cfg.Worker.PoolSize, metrics, logger, cfg.Worker.PollInterval)

	// Scheduler
	scheduler := qredis.NewScheduler(notifRepo, producer, logger)

	// Handlers
	notifHandler := handler.NewNotificationHandler(notifRepo, tmplRepo, producer, engine, logger)
	tmplHandler := handler.NewTemplateHandler(tmplRepo, logger)
	healthHandler := handler.NewHealthHandler(pool, rdb)
	metricsHandler := handler.NewMetricsHandler(metrics)
	wsHandler := handler.NewWebSocketHandler(hub, logger)

	// Router & server
	router := api.NewRouter(logger, notifHandler, tmplHandler, healthHandler, metricsHandler, wsHandler)
	server := api.NewServer(router, cfg.Server.Port, cfg.Server.ReadTimeout, cfg.Server.WriteTimeout, logger)

	// Start background services
	dispatcher.Start(ctx)
	go scheduler.Start(ctx)

	// Recovery sweep: re-enqueue pending notifications that may have been lost
	go func() {
		notifications, err := notifRepo.GetPendingForRecovery(ctx)
		if err != nil {
			logger.Error("recovery sweep failed", "error", err)
			return
		}
		for _, n := range notifications {
			msg := qredis.MessageFromNotification(n)
			if err := producer.Enqueue(ctx, msg); err != nil {
				logger.Error("recovery enqueue failed", "error", err, "id", n.ID)
			}
		}
		if len(notifications) > 0 {
			logger.Info("recovery sweep completed", "count", len(notifications))
		}
	}()

	// Start HTTP server
	go func() {
		if err := server.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server error", "error", err)
			cancel()
		}
	}()

	logger.Info("insider notification system started", "port", cfg.Server.Port)

	// Wait for shutdown signal
	<-ctx.Done()
	logger.Info("shutting down")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err)
	}

	dispatcher.Stop()
	logger.Info("shutdown complete")
	return nil
}
