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
	consumer     queue.Consumer
	rateLimiter  *qredis.RateLimiter
	processor    *Processor
	pools        map[domain.Channel]*Pool
	metrics      *observability.MetricsCollector
	logger       *slog.Logger
	pollInterval time.Duration
	batchSize    int
}

func NewDispatcher(
	consumer queue.Consumer,
	rateLimiter *qredis.RateLimiter,
	processor *Processor,
	poolSize int,
	metrics *observability.MetricsCollector,
	logger *slog.Logger,
	pollInterval time.Duration,
	batchSize int,
) *Dispatcher {
	pools := map[domain.Channel]*Pool{
		domain.ChannelSMS:   NewPool(poolSize),
		domain.ChannelEmail: NewPool(poolSize),
		domain.ChannelPush:  NewPool(poolSize),
	}

	if batchSize <= 0 {
		batchSize = 10
	}

	return &Dispatcher{
		consumer:     consumer,
		rateLimiter:  rateLimiter,
		processor:    processor,
		pools:        pools,
		metrics:      metrics,
		logger:       logger,
		pollInterval: pollInterval,
		batchSize:    batchSize,
	}
}

func (d *Dispatcher) Start(ctx context.Context) {
	for ch, pool := range d.pools {
		pool.Start(ctx)
		go d.dispatchLoop(ctx, ch)
		d.logger.Info("worker pool started", "channel", ch)
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

	msgs, err := d.consumer.DequeueBatch(ctx, string(channel), d.batchSize)
	if err != nil {
		d.logger.Error("dequeue batch failed", "error", err, "channel", channel)
		return
	}
	if len(msgs) == 0 {
		return
	}

	pool := d.pools[channel]
	for _, msg := range msgs {
		d.metrics.IncrProcessing(channel)
		m := msg // capture loop variable
		pool.Submit(func() {
			d.processor.Process(ctx, m)
		})
	}
}

func (d *Dispatcher) Stop() {
	for ch, pool := range d.pools {
		d.logger.Info("stopping worker pool", "channel", ch)
		pool.Stop()
	}
}

// NewSingleChannelDispatcher creates a dispatcher for a single channel.
// Used when running per-channel worker processes.
func NewSingleChannelDispatcher(
	consumer queue.Consumer,
	rateLimiter *qredis.RateLimiter,
	processor *Processor,
	channel domain.Channel,
	poolSize int,
	metrics *observability.MetricsCollector,
	logger *slog.Logger,
	pollInterval time.Duration,
	batchSize int,
) *Dispatcher {
	pools := map[domain.Channel]*Pool{
		channel: NewPool(poolSize),
	}

	if batchSize <= 0 {
		batchSize = 10
	}

	return &Dispatcher{
		consumer:     consumer,
		rateLimiter:  rateLimiter,
		processor:    processor,
		pools:        pools,
		metrics:      metrics,
		logger:       logger,
		pollInterval: pollInterval,
		batchSize:    batchSize,
	}
}

func (d *Dispatcher) Channels() []domain.Channel {
	channels := make([]domain.Channel, 0, len(d.pools))
	for ch := range d.pools {
		channels = append(channels, ch)
	}
	return channels
}
