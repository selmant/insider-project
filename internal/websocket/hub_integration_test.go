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

func TestWebSocket_ChannelFiltering(t *testing.T) {
	hub, srv := setupHubServer(t)

	// Connect two clients
	smsConn := connectWS(t, srv)
	allConn := connectWS(t, srv)

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 2, hub.Len())

	// Subscribe smsConn to only "sms"
	subMsg, _ := json.Marshal(ws.SubscribeMessage{
		Type:     "subscribe",
		Channels: []string{"sms"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := smsConn.Write(ctx, cws.MessageText, subMsg)
	require.NoError(t, err)

	// Give time for the subscribe to be processed
	time.Sleep(50 * time.Millisecond)

	// Broadcast an email event
	hub.Broadcast(ws.Event{
		Type:           "notification.sent",
		NotificationID: "email-123",
		Channel:        "email",
		Status:         "sent",
	})

	// allConn (no filter) should receive it
	ev := readEvent(t, allConn)
	assert.Equal(t, "email-123", ev.NotificationID)

	// smsConn should NOT receive the email event — instead broadcast sms
	hub.Broadcast(ws.Event{
		Type:           "notification.sent",
		NotificationID: "sms-456",
		Channel:        "sms",
		Status:         "sent",
	})

	// smsConn should receive the sms event
	ev = readEvent(t, smsConn)
	assert.Equal(t, "sms-456", ev.NotificationID)
	assert.Equal(t, "sms", ev.Channel)

	// allConn should also receive it
	ev = readEvent(t, allConn)
	assert.Equal(t, "sms-456", ev.NotificationID)
}

func TestWebSocket_SubscribeMultipleChannels(t *testing.T) {
	hub, srv := setupHubServer(t)

	conn := connectWS(t, srv)
	time.Sleep(50 * time.Millisecond)

	// Subscribe to sms and email
	subMsg, _ := json.Marshal(ws.SubscribeMessage{
		Type:     "subscribe",
		Channels: []string{"sms", "email"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, cws.MessageText, subMsg))
	time.Sleep(50 * time.Millisecond)

	// Broadcast push event — should be filtered out
	hub.Broadcast(ws.Event{
		Type:           "notification.sent",
		Channel:        "push",
		Status:         "sent",
		NotificationID: "push-1",
	})

	// Broadcast sms event — should be received
	hub.Broadcast(ws.Event{
		Type:           "notification.sent",
		Channel:        "sms",
		Status:         "sent",
		NotificationID: "sms-1",
	})

	ev := readEvent(t, conn)
	assert.Equal(t, "sms-1", ev.NotificationID)
	assert.Equal(t, "sms", ev.Channel)
}

func TestWebSocket_NoFilterReceivesAll(t *testing.T) {
	hub, srv := setupHubServer(t)
	conn := connectWS(t, srv)
	time.Sleep(50 * time.Millisecond)

	// No subscribe message sent — should receive all channels
	for _, ch := range []string{"sms", "email", "push"} {
		hub.Broadcast(ws.Event{
			Type:           "notification.sent",
			NotificationID: ch + "-msg",
			Channel:        ch,
			Status:         "sent",
		})

		ev := readEvent(t, conn)
		assert.Equal(t, ch+"-msg", ev.NotificationID)
		assert.Equal(t, ch, ev.Channel)
	}
}
