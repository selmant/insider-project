//go:build integration

package webhook_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/insider/insider/internal/domain"
	"github.com/insider/insider/internal/provider"
	"github.com/insider/insider/internal/provider/webhook"
)

func testNotification() *domain.Notification {
	return &domain.Notification{
		ID:        uuid.New(),
		Recipient: "+1234567890",
		Channel:   domain.ChannelSMS,
		Content:   "Hello integration test",
		Priority:  domain.PriorityNormal,
		Status:    domain.StatusProcessing,
	}
}

func TestWebhookClient_Success(t *testing.T) {
	var received json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := webhook.NewClient(srv.URL, 5*time.Second)
	n := testNotification()

	msgID, err := client.Send(context.Background(), n)
	require.NoError(t, err)
	assert.NotEmpty(t, msgID)

	// Verify payload
	var payload map[string]string
	require.NoError(t, json.Unmarshal(received, &payload))
	assert.Equal(t, n.ID.String(), payload["notification_id"])
	assert.Equal(t, n.Recipient, payload["recipient"])
	assert.Equal(t, string(n.Channel), payload["channel"])
	assert.Equal(t, n.Content, payload["content"])
	assert.NotEmpty(t, payload["timestamp"])
}

func TestWebhookClient_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := webhook.NewClient(srv.URL, 5*time.Second)
	_, err := client.Send(context.Background(), testNotification())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestCircuitBreaker_OpensAfterFailures(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := webhook.NewClient(srv.URL, 5*time.Second)
	cb := provider.NewCircuitBreaker(client, 3, 30*time.Second)

	n := testNotification()
	ctx := context.Background()

	// 3 failures to open the circuit
	for i := 0; i < 3; i++ {
		_, err := cb.Send(ctx, n)
		assert.Error(t, err)
	}
	assert.Equal(t, provider.StateOpen, cb.State())

	// Next call should be rejected without hitting the server
	callsBefore := calls.Load()
	_, err := cb.Send(ctx, n)
	assert.ErrorIs(t, err, domain.ErrCircuitOpen)
	assert.Equal(t, callsBefore, calls.Load(), "server should not be called when circuit is open")
}

func TestCircuitBreaker_HalfOpenRecovery(t *testing.T) {
	var shouldFail atomic.Bool
	shouldFail.Store(true)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if shouldFail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := webhook.NewClient(srv.URL, 5*time.Second)
	cb := provider.NewCircuitBreaker(client, 2, 100*time.Millisecond) // short timeout for test

	n := testNotification()
	ctx := context.Background()

	// Trip the circuit
	for i := 0; i < 2; i++ {
		cb.Send(ctx, n)
	}
	assert.Equal(t, provider.StateOpen, cb.State())

	// Wait for timeout to allow half-open
	time.Sleep(150 * time.Millisecond)

	// Fix the server
	shouldFail.Store(false)

	// Next call should succeed (half-open → closed)
	msgID, err := cb.Send(ctx, n)
	require.NoError(t, err)
	assert.NotEmpty(t, msgID)
	assert.Equal(t, provider.StateClosed, cb.State())
}
