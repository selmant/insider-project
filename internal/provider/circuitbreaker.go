package provider

import (
	"context"
	"sync"
	"time"

	"github.com/insider/insider/internal/domain"
)

type CBState int

const (
	StateClosed CBState = iota
	StateOpen
	StateHalfOpen
)

type CircuitBreaker struct {
	provider    Provider
	mu          sync.Mutex
	state       CBState
	failures    int
	maxFailures int
	timeout     time.Duration
	lastFailure time.Time
}

func NewCircuitBreaker(provider Provider, maxFailures int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		provider:    provider,
		state:       StateClosed,
		maxFailures: maxFailures,
		timeout:     timeout,
	}
}

func (cb *CircuitBreaker) Send(ctx context.Context, n *domain.Notification) (string, error) {
	cb.mu.Lock()

	switch cb.state {
	case StateOpen:
		if time.Since(cb.lastFailure) > cb.timeout {
			cb.state = StateHalfOpen
			cb.mu.Unlock()
			return cb.doSend(ctx, n)
		}
		cb.mu.Unlock()
		return "", domain.ErrCircuitOpen

	case StateHalfOpen:
		cb.mu.Unlock()
		return cb.doSend(ctx, n)

	default: // Closed
		cb.mu.Unlock()
		return cb.doSend(ctx, n)
	}
}

func (cb *CircuitBreaker) doSend(ctx context.Context, n *domain.Notification) (string, error) {
	msgID, err := cb.provider.Send(ctx, n)
	if err != nil {
		cb.recordFailure()
		return "", err
	}
	cb.recordSuccess()
	return msgID, nil
}

func (cb *CircuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	cb.lastFailure = time.Now()

	if cb.failures >= cb.maxFailures {
		cb.state = StateOpen
	}
}

func (cb *CircuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures = 0
	cb.state = StateClosed
}

func (cb *CircuitBreaker) State() CBState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}
