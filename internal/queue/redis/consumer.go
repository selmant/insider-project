package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/insider/insider/internal/queue"
)

const processingPrefix = "processing:"

type Consumer struct {
	client            *redis.Client
	visibilityTimeout time.Duration
}

func NewConsumer(client *redis.Client, visibilityTimeout time.Duration) *Consumer {
	return &Consumer{client: client, visibilityTimeout: visibilityTimeout}
}

func (c *Consumer) Dequeue(ctx context.Context, channel string) (*queue.Message, error) {
	key := queuePrefix + channel

	results, err := c.client.ZPopMin(ctx, key, 1).Result()
	if err != nil {
		return nil, fmt.Errorf("zpopmin: %w", err)
	}
	if len(results) == 0 {
		return nil, nil
	}

	raw := results[0].Member.(string)
	var msg queue.Message
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		return nil, fmt.Errorf("unmarshal message: %w", err)
	}

	deadline := float64(time.Now().Add(c.visibilityTimeout).UnixNano())
	procKey := processingPrefix + channel
	c.client.ZAdd(ctx, procKey, redis.Z{Score: deadline, Member: raw})

	return &msg, nil
}

func (c *Consumer) DequeueBatch(ctx context.Context, channel string, count int) ([]queue.Message, error) {
	key := queuePrefix + channel

	results, err := c.client.ZPopMin(ctx, key, int64(count)).Result()
	if err != nil {
		return nil, fmt.Errorf("zpopmin batch: %w", err)
	}
	if len(results) == 0 {
		return nil, nil
	}

	procKey := processingPrefix + channel
	deadline := float64(time.Now().Add(c.visibilityTimeout).UnixNano())

	pipe := c.client.Pipeline()
	msgs := make([]queue.Message, 0, len(results))
	for _, r := range results {
		raw := r.Member.(string)
		var msg queue.Message
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			return nil, fmt.Errorf("unmarshal message: %w", err)
		}
		msgs = append(msgs, msg)
		pipe.ZAdd(ctx, procKey, redis.Z{Score: deadline, Member: raw})
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("pipeline processing set: %w", err)
	}

	return msgs, nil
}

func (c *Consumer) Ack(ctx context.Context, channel string, msgs ...queue.Message) error {
	if len(msgs) == 0 {
		return nil
	}

	procKey := processingPrefix + channel
	members := make([]interface{}, len(msgs))
	for i, msg := range msgs {
		data, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("marshal message for ack: %w", err)
		}
		members[i] = string(data)
	}

	return c.client.ZRem(ctx, procKey, members...).Err()
}
