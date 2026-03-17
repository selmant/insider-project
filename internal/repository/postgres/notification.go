package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/insider/insider/internal/domain"
	"github.com/insider/insider/internal/repository"
)

type NotificationRepo struct {
	pool *pgxpool.Pool
}

func NewNotificationRepo(pool *pgxpool.Pool) *NotificationRepo {
	return &NotificationRepo{pool: pool}
}

func (r *NotificationRepo) Create(ctx context.Context, n *domain.Notification) error {
	varsJSON, err := json.Marshal(n.TemplateVars)
	if err != nil {
		return fmt.Errorf("marshal template vars: %w", err)
	}

	_, err = r.pool.Exec(ctx, insertNotification,
		n.ID, n.BatchID, n.IdempotencyKey, n.Recipient, n.Channel, n.Content,
		n.Priority, n.Status, n.TemplateID, varsJSON, n.ScheduledAt,
		n.Attempts, n.MaxAttempts, n.CreatedAt, n.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.ErrDuplicateKey
		}
		return fmt.Errorf("insert notification: %w", err)
	}
	return nil
}

func (r *NotificationRepo) CreateBatch(ctx context.Context, notifications []*domain.Notification) error {
	batch := &pgx.Batch{}
	for _, n := range notifications {
		varsJSON, err := json.Marshal(n.TemplateVars)
		if err != nil {
			return fmt.Errorf("marshal template vars: %w", err)
		}
		batch.Queue(insertNotification,
			n.ID, n.BatchID, n.IdempotencyKey, n.Recipient, n.Channel, n.Content,
			n.Priority, n.Status, n.TemplateID, varsJSON, n.ScheduledAt,
			n.Attempts, n.MaxAttempts, n.CreatedAt, n.UpdatedAt,
		)
	}

	br := r.pool.SendBatch(ctx, batch)
	defer br.Close()

	for range notifications {
		if _, err := br.Exec(); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return domain.ErrDuplicateKey
			}
			return fmt.Errorf("batch insert notification: %w", err)
		}
	}
	return nil
}

func (r *NotificationRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Notification, error) {
	n := &domain.Notification{}
	var varsJSON []byte

	err := r.pool.QueryRow(ctx, selectNotificationByID, id).Scan(
		&n.ID, &n.BatchID, &n.IdempotencyKey, &n.Recipient, &n.Channel, &n.Content,
		&n.Priority, &n.Status, &n.TemplateID, &varsJSON, &n.ScheduledAt,
		&n.ProviderMsgID, &n.Attempts, &n.MaxAttempts, &n.LastError, &n.SentAt,
		&n.CreatedAt, &n.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get notification: %w", err)
	}

	if varsJSON != nil {
		if err := json.Unmarshal(varsJSON, &n.TemplateVars); err != nil {
			return nil, fmt.Errorf("unmarshal template vars: %w", err)
		}
	}
	return n, nil
}

func (r *NotificationRepo) GetByBatchID(ctx context.Context, batchID uuid.UUID) ([]*domain.Notification, error) {
	rows, err := r.pool.Query(ctx, selectNotificationsByBatchID, batchID)
	if err != nil {
		return nil, fmt.Errorf("query batch notifications: %w", err)
	}
	defer rows.Close()
	return scanNotifications(rows)
}

func (r *NotificationRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.Status) error {
	_, err := r.pool.Exec(ctx, updateNotificationStatus, id, status)
	return err
}

func (r *NotificationRepo) UpdateStatusWithError(ctx context.Context, id uuid.UUID, status domain.Status, lastError string, attempts int) error {
	_, err := r.pool.Exec(ctx, updateNotificationStatusWithError, id, status, lastError, attempts)
	return err
}

func (r *NotificationRepo) UpdateSent(ctx context.Context, id uuid.UUID, providerMsgID string) error {
	_, err := r.pool.Exec(ctx, updateNotificationSent, id, providerMsgID)
	return err
}

func (r *NotificationRepo) List(ctx context.Context, filter repository.NotificationFilter) ([]*domain.Notification, int, error) {
	var conditions []string
	var args []interface{}
	argIdx := 1

	if filter.Channel != nil {
		conditions = append(conditions, fmt.Sprintf("channel = $%d", argIdx))
		args = append(args, *filter.Channel)
		argIdx++
	}
	if filter.Status != nil {
		conditions = append(conditions, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, *filter.Status)
		argIdx++
	}
	if filter.Priority != nil {
		conditions = append(conditions, fmt.Sprintf("priority = $%d", argIdx))
		args = append(args, *filter.Priority)
		argIdx++
	}

	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}

	// Count query
	countQuery := "SELECT COUNT(*) FROM notifications" + where
	var total int
	if err := r.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count notifications: %w", err)
	}

	// Data query
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	offset := filter.Offset

	query := fmt.Sprintf(`SELECT id, batch_id, idempotency_key, recipient, channel, content, priority, status,
		template_id, template_vars, scheduled_at, provider_msg_id, attempts, max_attempts,
		last_error, sent_at, created_at, updated_at
		FROM notifications%s ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, where, argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()

	notifications, err := scanNotifications(rows)
	if err != nil {
		return nil, 0, err
	}
	return notifications, total, nil
}

func (r *NotificationRepo) GetPendingScheduled(ctx context.Context) ([]*domain.Notification, error) {
	rows, err := r.pool.Query(ctx, selectPendingScheduled)
	if err != nil {
		return nil, fmt.Errorf("query pending scheduled: %w", err)
	}
	defer rows.Close()
	return scanNotifications(rows)
}

func (r *NotificationRepo) GetPendingForRecovery(ctx context.Context) ([]*domain.Notification, error) {
	rows, err := r.pool.Query(ctx, selectPendingForRecovery)
	if err != nil {
		return nil, fmt.Errorf("query pending for recovery: %w", err)
	}
	defer rows.Close()
	return scanNotifications(rows)
}

func scanNotifications(rows pgx.Rows) ([]*domain.Notification, error) {
	var result []*domain.Notification
	for rows.Next() {
		n := &domain.Notification{}
		var varsJSON []byte
		err := rows.Scan(
			&n.ID, &n.BatchID, &n.IdempotencyKey, &n.Recipient, &n.Channel, &n.Content,
			&n.Priority, &n.Status, &n.TemplateID, &varsJSON, &n.ScheduledAt,
			&n.ProviderMsgID, &n.Attempts, &n.MaxAttempts, &n.LastError, &n.SentAt,
			&n.CreatedAt, &n.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		if varsJSON != nil {
			if err := json.Unmarshal(varsJSON, &n.TemplateVars); err != nil {
				return nil, fmt.Errorf("unmarshal template vars: %w", err)
			}
		}
		result = append(result, n)
	}
	return result, rows.Err()
}
