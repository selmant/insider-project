package queue

import "context"

type Producer interface {
	Enqueue(ctx context.Context, msg Message) error
}

type Consumer interface {
	Dequeue(ctx context.Context, channel string) (*Message, error)
}
