package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/insider/insider/internal/domain"
	"github.com/insider/insider/internal/observability"
	"github.com/insider/insider/internal/provider"
	"github.com/insider/insider/internal/queue"
	"github.com/insider/insider/internal/repository"
	"github.com/insider/insider/internal/retry"
	ws "github.com/insider/insider/internal/websocket"
)

type Processor struct {
	repo      repository.NotificationRepository
	provider  provider.Provider
	retrier   *retry.Strategy
	deadLetter *retry.DeadLetterHandler
	hub       *ws.Hub
	metrics   *observability.MetricsCollector
	logger    *slog.Logger
}

func NewProcessor(
	repo repository.NotificationRepository,
	prov provider.Provider,
	retrier *retry.Strategy,
	deadLetter *retry.DeadLetterHandler,
	hub *ws.Hub,
	metrics *observability.MetricsCollector,
	logger *slog.Logger,
) *Processor {
	return &Processor{
		repo:       repo,
		provider:   prov,
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
			// Already processed, cancelled, or doesn't exist — skip silently
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

	p.metrics.IncrSent(n.Channel)
	p.hub.Broadcast(ws.Event{
		Type:           "notification.sent",
		NotificationID: n.ID.String(),
		Channel:        string(n.Channel),
		Status:         string(domain.StatusSent),
	})
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
	p.logger.Info("scheduling retry", "id", n.ID, "attempt", n.Attempts, "delay", delay)

	p.hub.Broadcast(ws.Event{
		Type:           "notification.retrying",
		NotificationID: n.ID.String(),
		Channel:        string(n.Channel),
		Status:         string(domain.StatusQueued),
		Error:          fmt.Sprintf("retry %d/%d in %s: %s", n.Attempts, n.MaxAttempts, delay, errMsg),
	})
}
