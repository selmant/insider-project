package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/insider/insider/internal/domain"
	"github.com/insider/insider/internal/observability"
	"github.com/insider/insider/internal/queue"
	qredis "github.com/insider/insider/internal/queue/redis"
)

type Dispatcher struct {
	consumer    queue.Consumer
	rateLimiter *qredis.RateLimiter
	processor   *Processor
	pools       map[domain.Channel]*Pool
	metrics     *observability.MetricsCollector
	logger      *slog.Logger
	pollInterval time.Duration
}

func NewDispatcher(
	consumer queue.Consumer,
	rateLimiter *qredis.RateLimiter,
	processor *Processor,
	poolSize int,
	metrics *observability.MetricsCollector,
	logger *slog.Logger,
	pollInterval time.Duration,
) *Dispatcher {
	pools := map[domain.Channel]*Pool{
		domain.ChannelSMS:   NewPool(poolSize),
		domain.ChannelEmail: NewPool(poolSize),
		domain.ChannelPush:  NewPool(poolSize),
	}

	return &Dispatcher{
		consumer:     consumer,
		rateLimiter:  rateLimiter,
		processor:    processor,
		pools:        pools,
		metrics:      metrics,
		logger:       logger,
		pollInterval: pollInterval,
	}
}

func (d *Dispatcher) Start(ctx context.Context) {
	// Start all worker pools
	for ch, pool := range d.pools {
		pool.Start(ctx)
		d.logger.Info("worker pool started", "channel", ch)
	}

	// Start a dispatch loop per channel
	for _, ch := range []domain.Channel{domain.ChannelSMS, domain.ChannelEmail, domain.ChannelPush} {
		go d.dispatchLoop(ctx, ch)
	}
}

func (d *Dispatcher) dispatchLoop(ctx context.Context, channel domain.Channel) {
	ticker := time.NewTicker(d.pollInterval)
	defer ticker.Stop()

	d.logger.Info("dispatch loop started", "channel", channel)

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("dispatch loop stopped", "channel", channel)
			return
		case <-ticker.C:
			d.dispatch(ctx, channel)
		}
	}
}

func (d *Dispatcher) dispatch(ctx context.Context, channel domain.Channel) {
	// Check rate limit
	allowed, err := d.rateLimiter.Allow(ctx, channel)
	if err != nil {
		d.logger.Error("rate limit check failed", "error", err, "channel", channel)
		return
	}
	if !allowed {
		return
	}

	msg, err := d.consumer.Dequeue(ctx, string(channel))
	if err != nil {
		d.logger.Error("dequeue failed", "error", err, "channel", channel)
		return
	}
	if msg == nil {
		return
	}

	d.metrics.IncrProcessing(channel)

	pool := d.pools[channel]
	pool.Submit(func() {
		d.processor.Process(ctx, *msg)
	})
}

func (d *Dispatcher) Stop() {
	for ch, pool := range d.pools {
		d.logger.Info("stopping worker pool", "channel", ch)
		pool.Stop()
	}
}
