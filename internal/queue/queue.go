package queue

import (
	"context"
	"time"
)

type Producer interface {
	Enqueue(ctx context.Context, msg Message) error
	EnqueueBatch(ctx context.Context, msgs []Message) error
	EnqueueDelayed(ctx context.Context, msg Message, delay time.Duration) error
}

type Consumer interface {
	Dequeue(ctx context.Context, channel string) (*Message, error)
	DequeueBatch(ctx context.Context, channel string, count int) ([]Message, error)
	Ack(ctx context.Context, channel string, msgs ...Message) error
}
