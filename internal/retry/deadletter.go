package retry

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DeadLetterHandler struct {
	pool *pgxpool.Pool
}

func NewDeadLetterHandler(pool *pgxpool.Pool) *DeadLetterHandler {
	return &DeadLetterHandler{pool: pool}
}

func (h *DeadLetterHandler) Handle(ctx context.Context, notificationID uuid.UUID, lastError string, attempts int) error {
	_, err := h.pool.Exec(ctx,
		`INSERT INTO dead_letters (id, notification_id, last_error, attempts, failed_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		uuid.New(), notificationID, lastError, attempts, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("insert dead letter: %w", err)
	}
	return nil
}
