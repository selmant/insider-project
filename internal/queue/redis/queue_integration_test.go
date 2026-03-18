//go:build integration

package redis_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/insider/insider/internal/domain"
	"github.com/insider/insider/internal/queue"
	qredis "github.com/insider/insider/internal/queue/redis"
	"github.com/insider/insider/internal/testutil"
)

var redisContainer *testutil.RedisContainer

func TestMain(m *testing.M) {
	ctx := context.Background()
	var err error
	redisContainer, err = testutil.SetupRedis(ctx)
	if err != nil {
		panic("failed to setup redis: " + err.Error())
	}
	defer redisContainer.Cleanup(ctx)
	m.Run()
}

func TestProducerConsumer_EnqueueDequeue(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, redisContainer.FlushAll(ctx))

	producer := qredis.NewProducer(redisContainer.Client)
	consumer := qredis.NewConsumer(redisContainer.Client)

	msg := queue.Message{
		NotificationID: uuid.New(),
		Channel:        domain.ChannelSMS,
		Priority:       domain.PriorityNormal,
	}

	err := producer.Enqueue(ctx, msg)
	require.NoError(t, err)

	got, err := consumer.Dequeue(ctx, string(domain.ChannelSMS))
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, msg.NotificationID, got.NotificationID)
	assert.Equal(t, msg.Channel, got.Channel)
	assert.Equal(t, msg.Priority, got.Priority)
}

func TestConsumer_EmptyQueue(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, redisContainer.FlushAll(ctx))

	consumer := qredis.NewConsumer(redisContainer.Client)

	got, err := consumer.Dequeue(ctx, string(domain.ChannelSMS))
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestProducerConsumer_PriorityOrdering(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, redisContainer.FlushAll(ctx))

	producer := qredis.NewProducer(redisContainer.Client)
	consumer := qredis.NewConsumer(redisContainer.Client)

	lowID := uuid.New()
	normalID := uuid.New()
	highID := uuid.New()

	// Enqueue in wrong order: low, normal, high
	require.NoError(t, producer.Enqueue(ctx, queue.Message{
		NotificationID: lowID,
		Channel:        domain.ChannelEmail,
		Priority:       domain.PriorityLow,
	}))
	time.Sleep(time.Millisecond) // ensure different timestamps
	require.NoError(t, producer.Enqueue(ctx, queue.Message{
		NotificationID: normalID,
		Channel:        domain.ChannelEmail,
		Priority:       domain.PriorityNormal,
	}))
	time.Sleep(time.Millisecond)
	require.NoError(t, producer.Enqueue(ctx, queue.Message{
		NotificationID: highID,
		Channel:        domain.ChannelEmail,
		Priority:       domain.PriorityHigh,
	}))

	// Dequeue should come back: high, normal, low
	msg1, err := consumer.Dequeue(ctx, string(domain.ChannelEmail))
	require.NoError(t, err)
	assert.Equal(t, highID, msg1.NotificationID)

	msg2, err := consumer.Dequeue(ctx, string(domain.ChannelEmail))
	require.NoError(t, err)
	assert.Equal(t, normalID, msg2.NotificationID)

	msg3, err := consumer.Dequeue(ctx, string(domain.ChannelEmail))
	require.NoError(t, err)
	assert.Equal(t, lowID, msg3.NotificationID)
}

func TestProducerConsumer_ChannelIsolation(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, redisContainer.FlushAll(ctx))

	producer := qredis.NewProducer(redisContainer.Client)
	consumer := qredis.NewConsumer(redisContainer.Client)

	smsID := uuid.New()
	emailID := uuid.New()

	require.NoError(t, producer.Enqueue(ctx, queue.Message{
		NotificationID: smsID,
		Channel:        domain.ChannelSMS,
		Priority:       domain.PriorityNormal,
	}))
	require.NoError(t, producer.Enqueue(ctx, queue.Message{
		NotificationID: emailID,
		Channel:        domain.ChannelEmail,
		Priority:       domain.PriorityNormal,
	}))

	// Dequeue from SMS should only get SMS message
	msg, err := consumer.Dequeue(ctx, string(domain.ChannelSMS))
	require.NoError(t, err)
	assert.Equal(t, smsID, msg.NotificationID)

	// SMS queue should now be empty
	msg, err = consumer.Dequeue(ctx, string(domain.ChannelSMS))
	require.NoError(t, err)
	assert.Nil(t, msg)

	// Email queue should still have its message
	msg, err = consumer.Dequeue(ctx, string(domain.ChannelEmail))
	require.NoError(t, err)
	assert.Equal(t, emailID, msg.NotificationID)
}

func TestRateLimiter_Allow(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, redisContainer.FlushAll(ctx))

	limiter := qredis.NewRateLimiter(redisContainer.Client, 5) // 5 per second

	// First 5 should be allowed
	for i := 0; i < 5; i++ {
		allowed, err := limiter.Allow(ctx, domain.ChannelSMS)
		require.NoError(t, err)
		assert.True(t, allowed, "request %d should be allowed", i)
	}

	// 6th should be denied
	allowed, err := limiter.Allow(ctx, domain.ChannelSMS)
	require.NoError(t, err)
	assert.False(t, allowed, "request should be rate limited")
}

func TestRateLimiter_ChannelIsolation(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, redisContainer.FlushAll(ctx))

	limiter := qredis.NewRateLimiter(redisContainer.Client, 2)

	// Exhaust SMS limit
	for i := 0; i < 2; i++ {
		allowed, err := limiter.Allow(ctx, domain.ChannelSMS)
		require.NoError(t, err)
		assert.True(t, allowed)
	}

	// SMS should be denied
	allowed, err := limiter.Allow(ctx, domain.ChannelSMS)
	require.NoError(t, err)
	assert.False(t, allowed)

	// Email should still be allowed (separate channel)
	allowed, err = limiter.Allow(ctx, domain.ChannelEmail)
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestRateLimiter_WindowExpiry(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, redisContainer.FlushAll(ctx))

	limiter := qredis.NewRateLimiter(redisContainer.Client, 2)

	// Exhaust limit
	for i := 0; i < 2; i++ {
		allowed, err := limiter.Allow(ctx, domain.ChannelPush)
		require.NoError(t, err)
		assert.True(t, allowed)
	}

	allowed, err := limiter.Allow(ctx, domain.ChannelPush)
	require.NoError(t, err)
	assert.False(t, allowed)

	// Wait for window to expire
	time.Sleep(1100 * time.Millisecond)

	// Should be allowed again
	allowed, err = limiter.Allow(ctx, domain.ChannelPush)
	require.NoError(t, err)
	assert.True(t, allowed)
}
