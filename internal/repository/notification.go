package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/insider/insider/internal/domain"
)

type NotificationFilter struct {
	Channel  *domain.Channel
	Status   *domain.Status
	Priority *domain.Priority
	Limit    int
	Offset   int
	Cursor   *time.Time // For keyset pagination: created_at < cursor
}

type NotificationRepository interface {
	Create(ctx context.Context, n *domain.Notification) error
	CreateBatch(ctx context.Context, notifications []*domain.Notification) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Notification, error)
	GetByBatchID(ctx context.Context, batchID uuid.UUID) ([]*domain.Notification, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status domain.Status) error
	UpdateStatusWithError(ctx context.Context, id uuid.UUID, status domain.Status, lastError string, attempts int) error
	UpdateSent(ctx context.Context, id uuid.UUID, providerMsgID string) error
	List(ctx context.Context, filter NotificationFilter) ([]*domain.Notification, int, error)
	GetPendingScheduled(ctx context.Context) ([]*domain.Notification, error)
	GetPendingForRecovery(ctx context.Context) ([]*domain.Notification, error)
	GetAndMarkProcessing(ctx context.Context, id uuid.UUID) (*domain.Notification, error)
	GetAndMarkProcessingBatch(ctx context.Context, ids []uuid.UUID) ([]*domain.Notification, error)
	UpdateBatchStatus(ctx context.Context, ids []uuid.UUID, status domain.Status) error
	UpdateBatchSent(ctx context.Context, ids []uuid.UUID, providerMsgIDs []string) error
}
