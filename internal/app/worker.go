package app

import (
	"context"
	"log/slog"

	"github.com/insider/insider/internal/domain"
	"github.com/insider/insider/internal/observability"
	"github.com/insider/insider/internal/provider"
	"github.com/insider/insider/internal/provider/webhook"
	qredis "github.com/insider/insider/internal/queue/redis"
	"github.com/insider/insider/internal/repository/postgres"
	"github.com/insider/insider/internal/retry"
	"github.com/insider/insider/internal/websocket"
	"github.com/insider/insider/internal/worker"
)

func RunWorker(ctx context.Context, infra *Infra, channel domain.Channel, logger *slog.Logger) error {
	cfg := infra.Config

	// Repositories
	notifRepo := postgres.NewNotificationRepo(infra.DB)

	// Queue
	producer := qredis.NewProducer(infra.Redis)
	consumer := qredis.NewConsumer(infra.Redis)
	rateLimiter := qredis.NewRateLimiter(infra.Redis, cfg.Worker.RateLimit)

	// WebSocket hub (for broadcasting status updates)
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

	// Retry + dead letter
	retrier := retry.NewStrategy(cfg.Worker.RetryBaseWait, cfg.Worker.RetryMaxWait)
	deadLetter := retry.NewDeadLetterHandler(infra.DB)

	// Processor
	processor := worker.NewProcessor(notifRepo, prov, retrier, deadLetter, hub, metrics, logger)

	// Single-channel dispatcher
	dispatcher := worker.NewSingleChannelDispatcher(
		consumer, rateLimiter, processor, channel,
		cfg.Worker.PoolSize, metrics, logger, cfg.Worker.PollInterval,
	)

	// Scheduler (each worker picks up scheduled notifications for its channel)
	scheduler := qredis.NewScheduler(notifRepo, producer, logger)

	// Recovery sweep for this channel
	go func() {
		notifications, err := notifRepo.GetPendingForRecovery(ctx)
		if err != nil {
			logger.Error("recovery sweep failed", "error", err)
			return
		}
		count := 0
		for _, n := range notifications {
			if n.Channel != channel {
				continue
			}
			msg := qredis.MessageFromNotification(n)
			if err := producer.Enqueue(ctx, msg); err != nil {
				logger.Error("recovery enqueue failed", "error", err, "id", n.ID)
				continue
			}
			count++
		}
		if count > 0 {
			logger.Info("recovery sweep completed", "channel", channel, "count", count)
		}
	}()

	// Start
	dispatcher.Start(ctx)
	go scheduler.Start(ctx)

	logger.Info("worker started", "channel", channel)

	<-ctx.Done()
	logger.Info("shutting down worker", "channel", channel)
	dispatcher.Stop()
	logger.Info("worker stopped", "channel", channel)
	return nil
}
