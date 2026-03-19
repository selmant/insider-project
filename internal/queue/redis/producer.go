package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/insider/insider/internal/queue"
)

const queuePrefix = "queue:"

type Producer struct {
	client *redis.Client
}

func NewProducer(client *redis.Client) *Producer {
	return &Producer{client: client}
}

func (p *Producer) Enqueue(ctx context.Context, msg queue.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	score := calculateScore(msg)
	key := queuePrefix + string(msg.Channel)

	return p.client.ZAdd(ctx, key, redis.Z{
		Score:  score,
		Member: string(data),
	}).Err()
}

func (p *Producer) EnqueueBatch(ctx context.Context, msgs []queue.Message) error {
	if len(msgs) == 0 {
		return nil
	}

	pipe := p.client.Pipeline()
	for _, msg := range msgs {
		data, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("marshal message: %w", err)
		}

		score := calculateScore(msg)
		key := queuePrefix + string(msg.Channel)

		pipe.ZAdd(ctx, key, redis.Z{
			Score:  score,
			Member: string(data),
		})
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("pipeline enqueue: %w", err)
	}
	return nil
}

// calculateScore produces a score for priority ordering.
// Lower score = higher priority. Score = (priorityLevel) * 1e12 + unixNano
func calculateScore(msg queue.Message) float64 {
	priorityLevel := float64(msg.Priority.Score())
	ts := float64(time.Now().UnixNano())
	if msg.ScheduledAt != nil {
		ts = float64(msg.ScheduledAt.UnixNano())
	}
	return priorityLevel*1e12 + ts
}
