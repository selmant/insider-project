//go:build integration

package redis_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
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

func TestConsumer_DequeueBatch(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, redisContainer.FlushAll(ctx))

	producer := qredis.NewProducer(redisContainer.Client)
	consumer := qredis.NewConsumer(redisContainer.Client)

	// Enqueue 5 messages
	ids := make([]uuid.UUID, 5)
	for i := range ids {
		ids[i] = uuid.New()
		require.NoError(t, producer.Enqueue(ctx, queue.Message{
			NotificationID: ids[i],
			Channel:        domain.ChannelSMS,
			Priority:       domain.PriorityNormal,
		}))
		time.Sleep(time.Millisecond) // ensure different timestamps/scores
	}

	// Batch dequeue 3
	msgs, err := consumer.DequeueBatch(ctx, string(domain.ChannelSMS), 3)
	require.NoError(t, err)
	assert.Len(t, msgs, 3)

	// Remaining 2
	msgs, err = consumer.DequeueBatch(ctx, string(domain.ChannelSMS), 10)
	require.NoError(t, err)
	assert.Len(t, msgs, 2)

	// Empty now
	msgs, err = consumer.DequeueBatch(ctx, string(domain.ChannelSMS), 5)
	require.NoError(t, err)
	assert.Nil(t, msgs)
}

func TestConsumer_DequeueBatch_EmptyQueue(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, redisContainer.FlushAll(ctx))

	consumer := qredis.NewConsumer(redisContainer.Client)

	msgs, err := consumer.DequeueBatch(ctx, string(domain.ChannelSMS), 10)
	require.NoError(t, err)
	assert.Nil(t, msgs)
}

func TestConsumer_DequeueBatch_PriorityOrdering(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, redisContainer.FlushAll(ctx))

	producer := qredis.NewProducer(redisContainer.Client)
	consumer := qredis.NewConsumer(redisContainer.Client)

	lowID := uuid.New()
	normalID := uuid.New()
	highID := uuid.New()

	require.NoError(t, producer.Enqueue(ctx, queue.Message{
		NotificationID: lowID,
		Channel:        domain.ChannelEmail,
		Priority:       domain.PriorityLow,
	}))
	time.Sleep(time.Millisecond)
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

	// Batch dequeue all 3 — should come out in priority order
	msgs, err := consumer.DequeueBatch(ctx, string(domain.ChannelEmail), 10)
	require.NoError(t, err)
	require.Len(t, msgs, 3)
	assert.Equal(t, highID, msgs[0].NotificationID)
	assert.Equal(t, normalID, msgs[1].NotificationID)
	assert.Equal(t, lowID, msgs[2].NotificationID)
}

func TestProducer_EnqueueBatch(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, redisContainer.FlushAll(ctx))

	producer := qredis.NewProducer(redisContainer.Client)
	consumer := qredis.NewConsumer(redisContainer.Client)

	msgs := []queue.Message{
		{NotificationID: uuid.New(), Channel: domain.ChannelSMS, Priority: domain.PriorityNormal},
		{NotificationID: uuid.New(), Channel: domain.ChannelSMS, Priority: domain.PriorityHigh},
		{NotificationID: uuid.New(), Channel: domain.ChannelSMS, Priority: domain.PriorityLow},
	}

	err := producer.EnqueueBatch(ctx, msgs)
	require.NoError(t, err)

	// Dequeue all — should be in priority order
	got, err := consumer.DequeueBatch(ctx, string(domain.ChannelSMS), 10)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, msgs[1].NotificationID, got[0].NotificationID) // high
	assert.Equal(t, msgs[0].NotificationID, got[1].NotificationID) // normal
	assert.Equal(t, msgs[2].NotificationID, got[2].NotificationID) // low
}

func TestProducer_EnqueueBatch_MultiChannel(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, redisContainer.FlushAll(ctx))

	producer := qredis.NewProducer(redisContainer.Client)
	consumer := qredis.NewConsumer(redisContainer.Client)

	msgs := []queue.Message{
		{NotificationID: uuid.New(), Channel: domain.ChannelSMS, Priority: domain.PriorityNormal},
		{NotificationID: uuid.New(), Channel: domain.ChannelEmail, Priority: domain.PriorityNormal},
		{NotificationID: uuid.New(), Channel: domain.ChannelPush, Priority: domain.PriorityNormal},
	}

	err := producer.EnqueueBatch(ctx, msgs)
	require.NoError(t, err)

	// Each channel should have exactly one message
	for _, ch := range []domain.Channel{domain.ChannelSMS, domain.ChannelEmail, domain.ChannelPush} {
		got, err := consumer.DequeueBatch(ctx, string(ch), 10)
		require.NoError(t, err)
		assert.Len(t, got, 1, "channel %s", ch)
	}
}

func TestProducer_EnqueueDelayed(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, redisContainer.FlushAll(ctx))

	producer := qredis.NewProducer(redisContainer.Client)
	consumer := qredis.NewConsumer(redisContainer.Client)

	msg := queue.Message{
		NotificationID: uuid.New(),
		Channel:        domain.ChannelSMS,
		Priority:       domain.PriorityNormal,
	}

	err := producer.EnqueueDelayed(ctx, msg, 5*time.Second)
	require.NoError(t, err)

	// Should NOT appear in the main queue
	got, err := consumer.Dequeue(ctx, string(domain.ChannelSMS))
	require.NoError(t, err)
	assert.Nil(t, got, "delayed message should not be in the main queue yet")

	// Should be in the retry:delayed sorted set
	count, err := redisContainer.Client.ZCard(ctx, "retry:delayed").Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestProducer_EnqueueDelayed_ReadyAfterDelay(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, redisContainer.FlushAll(ctx))

	producer := qredis.NewProducer(redisContainer.Client)

	msg := queue.Message{
		NotificationID: uuid.New(),
		Channel:        domain.ChannelEmail,
		Priority:       domain.PriorityHigh,
	}

	// Enqueue with a tiny delay so it's immediately ready
	err := producer.EnqueueDelayed(ctx, msg, 0)
	require.NoError(t, err)

	// The item should have a score <= now, meaning it's ready for promotion
	results, err := redisContainer.Client.ZRangeByScore(ctx, "retry:delayed", &goredis.ZRangeBy{
		Min: "-inf",
		Max: fmt.Sprintf("%d", time.Now().Add(time.Second).UnixNano()),
	}).Result()
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestProducer_EnqueueBatch_Empty(t *testing.T) {
	ctx := context.Background()

	producer := qredis.NewProducer(redisContainer.Client)

	err := producer.EnqueueBatch(ctx, nil)
	require.NoError(t, err)

	err = producer.EnqueueBatch(ctx, []queue.Message{})
	require.NoError(t, err)
}
