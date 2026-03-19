package queue

import "context"

type Producer interface {
	Enqueue(ctx context.Context, msg Message) error
	EnqueueBatch(ctx context.Context, msgs []Message) error
}

type Consumer interface {
	Dequeue(ctx context.Context, channel string) (*Message, error)
	DequeueBatch(ctx context.Context, channel string, count int) ([]Message, error)
}
