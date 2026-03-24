package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/insider/insider/internal/domain"
	"github.com/insider/insider/internal/observability"
	"github.com/insider/insider/internal/provider"
	"github.com/insider/insider/internal/queue"
	"github.com/insider/insider/internal/repository"
	"github.com/insider/insider/internal/retry"
	ws "github.com/insider/insider/internal/websocket"
)

type Processor struct {
	repo       repository.NotificationRepository
	provider   provider.Provider
	producer   queue.Producer
	consumer   queue.Consumer
	retrier    *retry.Strategy
	deadLetter *retry.DeadLetterHandler
	hub        *ws.Hub
	metrics    *observability.MetricsCollector
	logger     *slog.Logger
}

func NewProcessor(
	repo repository.NotificationRepository,
	prov provider.Provider,
	producer queue.Producer,
	consumer queue.Consumer,
	retrier *retry.Strategy,
	deadLetter *retry.DeadLetterHandler,
	hub *ws.Hub,
	metrics *observability.MetricsCollector,
	logger *slog.Logger,
) *Processor {
	return &Processor{
		repo:       repo,
		provider:   prov,
		producer:   producer,
		consumer:   consumer,
		retrier:    retrier,
		deadLetter: deadLetter,
		hub:        hub,
		metrics:    metrics,
		logger:     logger,
	}
}

func (p *Processor) Process(ctx context.Context, msg queue.Message) {
	n, err := p.repo.GetAndMarkProcessing(ctx, msg.NotificationID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			p.ack(ctx, msg)
			return
		}
		p.logger.Error("get and mark notification for processing", "error", err, "id", msg.NotificationID)
		return
	}

	// Send via provider
	providerMsgID, err := p.provider.Send(ctx, n)
	if err != nil {
		p.handleFailure(ctx, n, msg, err)
		return
	}

	// Success
	if err := p.repo.UpdateSent(ctx, n.ID, providerMsgID); err != nil {
		p.logger.Error("update sent status", "error", err, "id", n.ID)
		return
	}

	p.ack(ctx, msg)
	p.metrics.IncrSent(n.Channel)
	p.hub.Broadcast(ws.Event{
		Type:           "notification.sent",
		NotificationID: n.ID.String(),
		Channel:        string(n.Channel),
		Status:         string(domain.StatusSent),
	})
}

func (p *Processor) ProcessBatch(ctx context.Context, msgs []queue.Message) {
	if len(msgs) == 0 {
		return
	}

	// Collect IDs for batch DB fetch
	ids := make([]uuid.UUID, len(msgs))
	for i, m := range msgs {
		ids[i] = m.NotificationID
	}

	// Single DB call to get and mark all as processing
	notifications, err := p.repo.GetAndMarkProcessingBatch(ctx, ids)
	if err != nil {
		p.logger.Error("get and mark processing batch", "error", err)
		return
	}
	if len(notifications) == 0 {
		return
	}

	// Build a map from ID to message for retry handling
	msgMap := make(map[uuid.UUID]queue.Message, len(msgs))
	for _, m := range msgs {
		msgMap[m.NotificationID] = m
	}

	// Send each to provider individually (external API constraint)
	var sentIDs []uuid.UUID
	var sentProviderIDs []string
	var sentEvents []ws.Event

	for _, n := range notifications {
		providerMsgID, err := p.provider.Send(ctx, n)
		if err != nil {
			// handleFailure does its own DB updates and broadcasts for failures
			msg := msgMap[n.ID]
			p.handleFailure(ctx, n, msg, err)
			continue
		}

		sentIDs = append(sentIDs, n.ID)
		sentProviderIDs = append(sentProviderIDs, providerMsgID)
		p.metrics.IncrSent(n.Channel)
		sentEvents = append(sentEvents, ws.Event{
			Type:           "notification.sent",
			NotificationID: n.ID.String(),
			Channel:        string(n.Channel),
			Status:         string(domain.StatusSent),
		})
	}

	// Batch update sent statuses in one DB call
	if len(sentIDs) > 0 {
		if err := p.repo.UpdateBatchSent(ctx, sentIDs, sentProviderIDs); err != nil {
			p.logger.Error("batch update sent status", "error", err)
		}
	}

	// Ack all processed messages (sent + failed are both handled)
	if len(msgs) > 0 {
		p.ack(ctx, msgs...)
	}

	// Batch broadcast sent events
	if len(sentEvents) > 0 {
		p.hub.BroadcastBatch(sentEvents)
	}
}

func (p *Processor) handleFailure(ctx context.Context, n *domain.Notification, msg queue.Message, sendErr error) {
	n.Attempts++
	errMsg := sendErr.Error()

	p.logger.Warn("notification send failed",
		"id", n.ID,
		"channel", n.Channel,
		"attempt", n.Attempts,
		"error", errMsg,
	)

	p.metrics.IncrFailed(n.Channel)

	if n.Attempts >= n.MaxAttempts {
		// Dead letter
		if err := p.repo.UpdateStatusWithError(ctx, n.ID, domain.StatusFailed, errMsg, n.Attempts); err != nil {
			p.logger.Error("update failed status", "error", err, "id", n.ID)
		}
		if err := p.deadLetter.Handle(ctx, n.ID, errMsg, n.Attempts); err != nil {
			p.logger.Error("dead letter", "error", err, "id", n.ID)
		}

		p.ack(ctx, msg)
		p.hub.Broadcast(ws.Event{
			Type:           "notification.failed",
			NotificationID: n.ID.String(),
			Channel:        string(n.Channel),
			Status:         string(domain.StatusFailed),
			Error:          errMsg,
		})
		return
	}

	// Retry with backoff
	if err := p.repo.UpdateStatusWithError(ctx, n.ID, domain.StatusQueued, errMsg, n.Attempts); err != nil {
		p.logger.Error("update retry status", "error", err, "id", n.ID)
		return
	}

	delay := p.retrier.NextDelay(n.Attempts)
	if err := p.producer.EnqueueDelayed(ctx, msg, delay); err != nil {
		p.logger.Error("retry enqueue failed", "error", err, "id", n.ID)
		return
	}
	p.ack(ctx, msg)
	p.logger.Info("scheduled retry", "id", n.ID, "attempt", n.Attempts, "delay", delay)

	p.hub.Broadcast(ws.Event{
		Type:           "notification.retrying",
		NotificationID: n.ID.String(),
		Channel:        string(n.Channel),
		Status:         string(domain.StatusQueued),
		Error:          fmt.Sprintf("retry %d/%d in %s: %s", n.Attempts, n.MaxAttempts, delay, errMsg),
	})
}

func (p *Processor) ack(ctx context.Context, msgs ...queue.Message) {
	if len(msgs) == 0 {
		return
	}
	ch := string(msgs[0].Channel)
	if err := p.consumer.Ack(ctx, ch, msgs...); err != nil {
		p.logger.Error("ack failed", "error", err, "channel", ch)
	}
}
