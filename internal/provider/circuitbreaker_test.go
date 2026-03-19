package provider

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/insider/insider/internal/domain"
)

type mockProvider struct {
	err error
}

func (m *mockProvider) Send(_ context.Context, _ *domain.Notification) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return "msg-123", nil
}

func TestCircuitBreaker_ClosedOnSuccess(t *testing.T) {
	cb := NewCircuitBreaker(&mockProvider{}, 3, 30*time.Second)

	msgID, err := cb.Send(context.Background(), &domain.Notification{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msgID != "msg-123" {
		t.Errorf("got msgID %q, want %q", msgID, "msg-123")
	}
	if cb.State() != StateClosed {
		t.Errorf("state = %d, want StateClosed", cb.State())
	}
}

func TestCircuitBreaker_OpensAfterMaxFailures(t *testing.T) {
	provErr := errors.New("provider down")
	cb := NewCircuitBreaker(&mockProvider{err: provErr}, 3, 30*time.Second)

	for i := 0; i < 3; i++ {
		_, err := cb.Send(context.Background(), &domain.Notification{})
		if err == nil {
			t.Fatal("expected error")
		}
	}

	if cb.State() != StateOpen {
		t.Errorf("state = %d, want StateOpen", cb.State())
	}

	// Should return circuit open error now
	_, err := cb.Send(context.Background(), &domain.Notification{})
	if !errors.Is(err, domain.ErrCircuitOpen) {
		t.Errorf("got %v, want ErrCircuitOpen", err)
	}
}

func TestCircuitBreaker_HalfOpenAfterTimeout(t *testing.T) {
	provErr := errors.New("provider down")
	mock := &mockProvider{err: provErr}
	cb := NewCircuitBreaker(mock, 2, 50*time.Millisecond)

	// Open the circuit
	for i := 0; i < 2; i++ {
		_, _ = cb.Send(context.Background(), &domain.Notification{})
	}
	if cb.State() != StateOpen {
		t.Fatal("expected open state")
	}

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// Fix the provider
	mock.err = nil

	// Should transition to half-open then closed on success
	msgID, err := cb.Send(context.Background(), &domain.Notification{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msgID != "msg-123" {
		t.Errorf("got msgID %q, want %q", msgID, "msg-123")
	}
	if cb.State() != StateClosed {
		t.Errorf("state = %d, want StateClosed", cb.State())
	}
}
