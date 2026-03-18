//go:build integration

package worker_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/insider/insider/internal/domain"
	qredis "github.com/insider/insider/internal/queue/redis"
	"github.com/insider/insider/internal/repository/postgres"
)

// recoverySweep replicates the recovery logic from internal/app/worker.go
// to test it in isolation without wiring up the full worker.
func recoverySweep(ctx context.Context, repo *postgres.NotificationRepo, producer *qredis.Producer, channel domain.Channel) (int, error) {
	notifications, err := repo.GetPendingForRecovery(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, n := range notifications {
		if n.Channel != channel {
			continue
		}
		msg := qredis.MessageFromNotification(n)
		if err := producer.Enqueue(ctx, msg); err != nil {
			continue
		}
		count++
	}
	return count, nil
}

func TestRecovery_ReenqueuesPendingAndQueued(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, pgContainer.TruncateTables(ctx))
	require.NoError(t, redisContainer.FlushAll(ctx))

	repo := postgres.NewNotificationRepo(pgContainer.Pool)
	producer := qredis.NewProducer(redisContainer.Client)
	consumer := qredis.NewConsumer(redisContainer.Client)

	// Create stuck notifications
	n1 := newTestNotification(domain.ChannelSMS, domain.StatusPending)
	n2 := newTestNotification(domain.ChannelSMS, domain.StatusQueued)
	require.NoError(t, repo.Create(ctx, n1))
	require.NoError(t, repo.Create(ctx, n2))

	count, err := recoverySweep(ctx, repo, producer, domain.ChannelSMS)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// Both should be in the Redis queue
	msg1, err := consumer.Dequeue(ctx, string(domain.ChannelSMS))
	require.NoError(t, err)
	require.NotNil(t, msg1)

	msg2, err := consumer.Dequeue(ctx, string(domain.ChannelSMS))
	require.NoError(t, err)
	require.NotNil(t, msg2)

	// Queue should be empty now
	msg3, err := consumer.Dequeue(ctx, string(domain.ChannelSMS))
	require.NoError(t, err)
	assert.Nil(t, msg3)
}

func TestRecovery_SkipsTerminalStatuses(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, pgContainer.TruncateTables(ctx))
	require.NoError(t, redisContainer.FlushAll(ctx))

	repo := postgres.NewNotificationRepo(pgContainer.Pool)
	producer := qredis.NewProducer(redisContainer.Client)
	consumer := qredis.NewConsumer(redisContainer.Client)

	// Create terminal notifications
	n1 := newTestNotification(domain.ChannelEmail, domain.StatusSent)
	n2 := newTestNotification(domain.ChannelEmail, domain.StatusFailed)
	n3 := newTestNotification(domain.ChannelEmail, domain.StatusCancelled)
	require.NoError(t, repo.Create(ctx, n1))
	require.NoError(t, repo.Create(ctx, n2))
	require.NoError(t, repo.Create(ctx, n3))

	count, err := recoverySweep(ctx, repo, producer, domain.ChannelEmail)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// Queue should be empty
	msg, err := consumer.Dequeue(ctx, string(domain.ChannelEmail))
	require.NoError(t, err)
	assert.Nil(t, msg)
}

func TestRecovery_FiltersbyChannel(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, pgContainer.TruncateTables(ctx))
	require.NoError(t, redisContainer.FlushAll(ctx))

	repo := postgres.NewNotificationRepo(pgContainer.Pool)
	producer := qredis.NewProducer(redisContainer.Client)
	consumer := qredis.NewConsumer(redisContainer.Client)

	// Create stuck notifications across channels
	n1 := newTestNotification(domain.ChannelSMS, domain.StatusPending)
	n2 := newTestNotification(domain.ChannelEmail, domain.StatusPending)
	n3 := newTestNotification(domain.ChannelPush, domain.StatusPending)
	require.NoError(t, repo.Create(ctx, n1))
	require.NoError(t, repo.Create(ctx, n2))
	require.NoError(t, repo.Create(ctx, n3))

	// Recovery for SMS only
	count, err := recoverySweep(ctx, repo, producer, domain.ChannelSMS)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Only SMS queue should have a message
	msg, err := consumer.Dequeue(ctx, string(domain.ChannelSMS))
	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.Equal(t, n1.ID, msg.NotificationID)

	// Email and push queues should be empty
	msg, err = consumer.Dequeue(ctx, string(domain.ChannelEmail))
	require.NoError(t, err)
	assert.Nil(t, msg)

	msg, err = consumer.Dequeue(ctx, string(domain.ChannelPush))
	require.NoError(t, err)
	assert.Nil(t, msg)
}
