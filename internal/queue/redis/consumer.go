package redis

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/insider/insider/internal/queue"
)

type Consumer struct {
	client *redis.Client
}

func NewConsumer(client *redis.Client) *Consumer {
	return &Consumer{client: client}
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

	var msg queue.Message
	if err := json.Unmarshal([]byte(results[0].Member.(string)), &msg); err != nil {
		return nil, fmt.Errorf("unmarshal message: %w", err)
	}

	return &msg, nil
}
