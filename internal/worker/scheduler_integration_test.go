//go:build integration

package worker_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/insider/insider/internal/domain"
	"github.com/insider/insider/internal/queue"
	qredis "github.com/insider/insider/internal/queue/redis"
	"github.com/insider/insider/internal/repository/postgres"
)

func TestScheduler_EnqueuesDueNotifications(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, pgContainer.TruncateTables(ctx))
	require.NoError(t, redisContainer.FlushAll(ctx))

	repo := postgres.NewNotificationRepo(pgContainer.Pool)
	producer := qredis.NewProducer(redisContainer.Client)
	consumer := qredis.NewConsumer(redisContainer.Client, 30*time.Second)

	// Create a scheduled notification due in the past
	past := time.Now().Add(-1 * time.Minute).Truncate(time.Microsecond)
	n := newTestNotification(domain.ChannelSMS, domain.StatusScheduled)
	n.ScheduledAt = &past
	require.NoError(t, repo.Create(ctx, n))

	// Run scheduler for a short period
	scheduler := qredis.NewScheduler(repo, producer, redisContainer.Client, slog.Default())
	sctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	scheduler.Start(sctx)

	// Verify: notification status updated to queued
	got, err := repo.GetByID(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusQueued, got.Status)

	// Verify: message in Redis queue
	msg, err := consumer.Dequeue(ctx, string(domain.ChannelSMS))
	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.Equal(t, n.ID, msg.NotificationID)
}

func TestScheduler_IgnoresFutureNotifications(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, pgContainer.TruncateTables(ctx))
	require.NoError(t, redisContainer.FlushAll(ctx))

	repo := postgres.NewNotificationRepo(pgContainer.Pool)
	producer := qredis.NewProducer(redisContainer.Client)
	consumer := qredis.NewConsumer(redisContainer.Client, 30*time.Second)

	// Create a scheduled notification in the future
	future := time.Now().Add(1 * time.Hour).Truncate(time.Microsecond)
	n := newTestNotification(domain.ChannelEmail, domain.StatusScheduled)
	n.ScheduledAt = &future
	require.NoError(t, repo.Create(ctx, n))

	// Run scheduler briefly
	scheduler := qredis.NewScheduler(repo, producer, redisContainer.Client, slog.Default())
	sctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	scheduler.Start(sctx)

	// Status should still be scheduled
	got, err := repo.GetByID(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusScheduled, got.Status)

	// Queue should be empty
	msg, err := consumer.Dequeue(ctx, string(domain.ChannelEmail))
	require.NoError(t, err)
	assert.Nil(t, msg)
}

func TestScheduler_PromotesDelayedRetries(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, pgContainer.TruncateTables(ctx))
	require.NoError(t, redisContainer.FlushAll(ctx))

	producer := qredis.NewProducer(redisContainer.Client)
	consumer := qredis.NewConsumer(redisContainer.Client, 30*time.Second)
	repo := postgres.NewNotificationRepo(pgContainer.Pool)

	// Put a message into the delayed retry set with zero delay (immediately ready)
	msg := queue.Message{
		NotificationID: uuid.New(),
		Channel:        domain.ChannelSMS,
		Priority:       domain.PriorityNormal,
	}
	require.NoError(t, producer.EnqueueDelayed(ctx, msg, 0))

	// Main queue should be empty before scheduler runs
	got, err := consumer.Dequeue(ctx, string(domain.ChannelSMS))
	require.NoError(t, err)
	assert.Nil(t, got)

	// Run scheduler — it should promote the delayed message
	scheduler := qredis.NewScheduler(repo, producer, redisContainer.Client, slog.Default())
	sctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	scheduler.Start(sctx)

	// Message should now be in the main queue
	got, err = consumer.Dequeue(ctx, string(domain.ChannelSMS))
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, msg.NotificationID, got.NotificationID)

	// Delayed set should be empty
	count, err := redisContainer.Client.ZCard(ctx, "retry:delayed").Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

func TestScheduler_SkipsNotYetReadyDelayed(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, pgContainer.TruncateTables(ctx))
	require.NoError(t, redisContainer.FlushAll(ctx))

	producer := qredis.NewProducer(redisContainer.Client)
	consumer := qredis.NewConsumer(redisContainer.Client, 30*time.Second)
	repo := postgres.NewNotificationRepo(pgContainer.Pool)

	// Put a message with a long delay (not ready yet)
	msg := queue.Message{
		NotificationID: uuid.New(),
		Channel:        domain.ChannelEmail,
		Priority:       domain.PriorityHigh,
	}
	require.NoError(t, producer.EnqueueDelayed(ctx, msg, 1*time.Hour))

	// Run scheduler briefly
	scheduler := qredis.NewScheduler(repo, producer, redisContainer.Client, slog.Default())
	sctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	scheduler.Start(sctx)

	// Main queue should still be empty
	got, err := consumer.Dequeue(ctx, string(domain.ChannelEmail))
	require.NoError(t, err)
	assert.Nil(t, got)

	// Delayed set should still have the message
	count, err := redisContainer.Client.ZCard(ctx, "retry:delayed").Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestScheduler_IgnoresNonScheduled(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, pgContainer.TruncateTables(ctx))
	require.NoError(t, redisContainer.FlushAll(ctx))

	repo := postgres.NewNotificationRepo(pgContainer.Pool)
	producer := qredis.NewProducer(redisContainer.Client)
	consumer := qredis.NewConsumer(redisContainer.Client, 30*time.Second)

	// Create pending and sent notifications (not scheduled)
	n1 := newTestNotification(domain.ChannelPush, domain.StatusPending)
	n2 := newTestNotification(domain.ChannelPush, domain.StatusSent)
	require.NoError(t, repo.Create(ctx, n1))
	require.NoError(t, repo.Create(ctx, n2))

	scheduler := qredis.NewScheduler(repo, producer, redisContainer.Client, slog.Default())
	sctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	scheduler.Start(sctx)

	// Queue should be empty — scheduler only picks up 'scheduled' status
	msg, err := consumer.Dequeue(ctx, string(domain.ChannelPush))
	require.NoError(t, err)
	assert.Nil(t, msg)
}
