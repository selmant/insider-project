//go:build integration

package websocket_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	cws "github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ws "github.com/insider/insider/internal/websocket"
)

func setupHubServer(t *testing.T) (*ws.Hub, *httptest.Server) {
	t.Helper()
	hub := ws.NewHub(slog.Default())
	// Use a long-lived context for websocket connections so they outlive the HTTP handler.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := cws.Accept(w, r, &cws.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		hub.Register(conn, ctx)
	}))
	t.Cleanup(srv.Close)
	return hub, srv
}

func connectWS(t *testing.T, srv *httptest.Server) *cws.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + srv.URL[4:] // http -> ws
	conn, _, err := cws.Dial(ctx, url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

func readEvent(t *testing.T, conn *cws.Conn) ws.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	require.NoError(t, err)
	var ev ws.Event
	require.NoError(t, json.Unmarshal(data, &ev))
	return ev
}

func TestWebSocket_ReceivesBroadcast(t *testing.T) {
	hub, srv := setupHubServer(t)
	conn := connectWS(t, srv)

	// Wait for registration
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, hub.Len())

	// Broadcast an event
	hub.Broadcast(ws.Event{
		Type:           "status_update",
		NotificationID: "test-id-123",
		Channel:        "sms",
		Status:         "sent",
	})

	ev := readEvent(t, conn)
	assert.Equal(t, "status_update", ev.Type)
	assert.Equal(t, "test-id-123", ev.NotificationID)
	assert.Equal(t, "sms", ev.Channel)
	assert.Equal(t, "sent", ev.Status)
}

func TestWebSocket_MultipleClients(t *testing.T) {
	hub, srv := setupHubServer(t)

	conns := make([]*cws.Conn, 3)
	for i := range conns {
		conns[i] = connectWS(t, srv)
	}

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 3, hub.Len())

	hub.Broadcast(ws.Event{
		Type:           "status_update",
		NotificationID: "multi-test",
		Channel:        "email",
		Status:         "failed",
		Error:          "provider timeout",
	})

	for i, conn := range conns {
		ev := readEvent(t, conn)
		assert.Equal(t, "multi-test", ev.NotificationID, "client %d", i)
		assert.Equal(t, "provider timeout", ev.Error, "client %d", i)
	}
}

func TestWebSocket_Disconnect(t *testing.T) {
	hub, srv := setupHubServer(t)
	conn := connectWS(t, srv)

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, hub.Len())

	// Close connection
	conn.Close(cws.StatusNormalClosure, "bye")

	// Wait for unregistration
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 0, hub.Len())

	// Broadcast should not panic with zero clients
	hub.Broadcast(ws.Event{
		Type:   "status_update",
		Status: "sent",
	})
}
