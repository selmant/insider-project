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
			// Eager polling: if we got a full batch, immediately poll again
			for {
				n := d.dispatch(ctx, channel)
				if n < d.batchSize {
					break
				}
				// Check context before re-polling
				if ctx.Err() != nil {
					return
				}
			}
		}
	}
}

// dispatch dequeues and processes a batch. Returns the number of messages processed.
func (d *Dispatcher) dispatch(ctx context.Context, channel domain.Channel) int {
	// Check rate limit for the full batch size
	allowed, err := d.rateLimiter.AllowN(ctx, channel, d.batchSize)
	if err != nil {
		d.logger.Error("rate limit check failed", "error", err, "channel", channel)
		return 0
	}
	if allowed == 0 {
		return 0
	}

	msgs, err := d.consumer.DequeueBatch(ctx, string(channel), allowed)
	if err != nil {
		d.logger.Error("dequeue batch failed", "error", err, "channel", channel)
		return 0
	}
	if len(msgs) == 0 {
		return 0
	}

	for range msgs {
		d.metrics.IncrProcessing(channel)
	}

	pool := d.pools[channel]
	batch := msgs // capture for closure
	pool.Submit(func() {
		d.processor.ProcessBatch(ctx, batch)
	})

	return len(msgs)
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
