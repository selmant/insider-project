//go:build integration

package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/insider/insider/internal/api"
	"github.com/insider/insider/internal/api/handler"
	"github.com/insider/insider/internal/domain"
	"github.com/insider/insider/internal/observability"
	qredis "github.com/insider/insider/internal/queue/redis"
	"github.com/insider/insider/internal/repository/postgres"
	"github.com/insider/insider/internal/template"
	"github.com/insider/insider/internal/testutil"
	"github.com/insider/insider/internal/websocket"
)

var (
	pgContainer    *testutil.PostgresContainer
	redisContainer *testutil.RedisContainer
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	var err error

	pgContainer, err = testutil.SetupPostgres(ctx)
	if err != nil {
		panic("failed to setup postgres: " + err.Error())
	}
	defer pgContainer.Cleanup(ctx)

	redisContainer, err = testutil.SetupRedis(ctx)
	if err != nil {
		panic("failed to setup redis: " + err.Error())
	}
	defer redisContainer.Cleanup(ctx)

	m.Run()
}

type testServer struct {
	handler http.Handler
	server  *httptest.Server
}

func setupTestServer(t *testing.T) *testServer {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, pgContainer.TruncateTables(ctx))
	require.NoError(t, redisContainer.FlushAll(ctx))

	logger := slog.Default()
	notifRepo := postgres.NewNotificationRepo(pgContainer.Pool)
	tmplRepo := postgres.NewTemplateRepo(pgContainer.Pool)
	producer := qredis.NewProducer(redisContainer.Client)
	engine := template.NewEngine()
	hub := websocket.NewHub(logger)
	collector := observability.NewMetricsCollector(hub.Len)

	notifHandler := handler.NewNotificationHandler(notifRepo, tmplRepo, producer, engine, logger)
	tmplHandler := handler.NewTemplateHandler(tmplRepo, logger)
	healthHandler := handler.NewHealthHandler(pgContainer.Pool, redisContainer.Client)
	metricsHandler := handler.NewMetricsHandler(collector)
	wsHandler := handler.NewWebSocketHandler(hub, logger)

	router := api.NewRouter(logger, notifHandler, tmplHandler, healthHandler, metricsHandler, wsHandler)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return &testServer{handler: router, server: srv}
}

func (ts *testServer) do(t *testing.T, method, path string, body interface{}) *http.Response {
	t.Helper()
	var reqBody *bytes.Buffer
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reqBody = bytes.NewBuffer(data)
	} else {
		reqBody = &bytes.Buffer{}
	}

	req, err := http.NewRequest(method, ts.server.URL+path, reqBody)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, v interface{}) {
	t.Helper()
	defer resp.Body.Close()
	require.NoError(t, json.NewDecoder(resp.Body).Decode(v))
}

// --- Health ---

func TestAPI_Health(t *testing.T) {
	ts := setupTestServer(t)

	resp := ts.do(t, "GET", "/health", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	decodeJSON(t, resp, &body)
	assert.Equal(t, "ok", body["status"])
}

func TestAPI_Ready(t *testing.T) {
	ts := setupTestServer(t)

	resp := ts.do(t, "GET", "/ready", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	decodeJSON(t, resp, &body)
	assert.Equal(t, "ok", body["status"])
	assert.Equal(t, "up", body["postgres"])
	assert.Equal(t, "up", body["redis"])
}

// --- Notifications ---

func TestAPI_CreateNotification(t *testing.T) {
	ts := setupTestServer(t)

	resp := ts.do(t, "POST", "/api/v1/notifications", map[string]interface{}{
		"recipient": "+1234567890",
		"channel":   "sms",
		"content":   "Hello World",
		"priority":  "high",
	})
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var n domain.Notification
	decodeJSON(t, resp, &n)
	assert.Equal(t, "+1234567890", n.Recipient)
	assert.Equal(t, domain.ChannelSMS, n.Channel)
	assert.Equal(t, "Hello World", n.Content)
	assert.Equal(t, domain.PriorityHigh, n.Priority)
	// The response returns the notification before enqueue updates status in DB,
	// so the response body still shows "pending". The DB status is "queued".
	assert.Equal(t, domain.StatusPending, n.Status)
}

func TestAPI_CreateNotification_DefaultPriority(t *testing.T) {
	ts := setupTestServer(t)

	resp := ts.do(t, "POST", "/api/v1/notifications", map[string]interface{}{
		"recipient": "user@example.com",
		"channel":   "email",
		"content":   "Test email",
	})
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var n domain.Notification
	decodeJSON(t, resp, &n)
	assert.Equal(t, domain.PriorityNormal, n.Priority)
}

func TestAPI_CreateNotification_Validation(t *testing.T) {
	ts := setupTestServer(t)

	tests := []struct {
		name string
		body map[string]interface{}
	}{
		{"missing recipient", map[string]interface{}{"channel": "sms", "content": "hi"}},
		{"invalid channel", map[string]interface{}{"recipient": "+1", "channel": "fax", "content": "hi"}},
		{"missing content", map[string]interface{}{"recipient": "+1", "channel": "sms"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := ts.do(t, "POST", "/api/v1/notifications", tt.body)
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			resp.Body.Close()
		})
	}
}

func TestAPI_CreateNotification_IdempotencyKey(t *testing.T) {
	ts := setupTestServer(t)

	body := map[string]interface{}{
		"recipient":       "+1234567890",
		"channel":         "sms",
		"content":         "Hello",
		"idempotency_key": "key-123",
	}

	resp := ts.do(t, "POST", "/api/v1/notifications", body)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// Second request with same key should 409
	resp = ts.do(t, "POST", "/api/v1/notifications", body)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	resp.Body.Close()
}

func TestAPI_CreateNotification_Scheduled(t *testing.T) {
	ts := setupTestServer(t)

	resp := ts.do(t, "POST", "/api/v1/notifications", map[string]interface{}{
		"recipient":    "+1234567890",
		"channel":      "push",
		"content":      "Scheduled msg",
		"scheduled_at": "2099-01-01T00:00:00Z",
	})
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var n domain.Notification
	decodeJSON(t, resp, &n)
	assert.Equal(t, domain.StatusScheduled, n.Status) // scheduled, not queued
}

func TestAPI_GetNotification(t *testing.T) {
	ts := setupTestServer(t)

	// Create first
	resp := ts.do(t, "POST", "/api/v1/notifications", map[string]interface{}{
		"recipient": "+1234567890",
		"channel":   "sms",
		"content":   "Hello",
	})
	var created domain.Notification
	decodeJSON(t, resp, &created)

	// Get by ID
	resp = ts.do(t, "GET", "/api/v1/notifications/"+created.ID.String(), nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var got domain.Notification
	decodeJSON(t, resp, &got)
	assert.Equal(t, created.ID, got.ID)
}

func TestAPI_GetNotification_NotFound(t *testing.T) {
	ts := setupTestServer(t)

	resp := ts.do(t, "GET", "/api/v1/notifications/00000000-0000-0000-0000-000000000000", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func TestAPI_CancelNotification(t *testing.T) {
	ts := setupTestServer(t)

	// Create a scheduled notification (cancellable)
	resp := ts.do(t, "POST", "/api/v1/notifications", map[string]interface{}{
		"recipient":    "+1234567890",
		"channel":      "sms",
		"content":      "Cancel me",
		"scheduled_at": "2099-01-01T00:00:00Z",
	})
	var created domain.Notification
	decodeJSON(t, resp, &created)
	assert.Equal(t, domain.StatusScheduled, created.Status)

	// Cancel
	resp = ts.do(t, "POST", fmt.Sprintf("/api/v1/notifications/%s/cancel", created.ID), nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var cancelled domain.Notification
	decodeJSON(t, resp, &cancelled)
	assert.Equal(t, domain.StatusCancelled, cancelled.Status)
}

func TestAPI_CancelNotification_AlreadySent(t *testing.T) {
	ts := setupTestServer(t)

	// Create notification (gets queued immediately)
	resp := ts.do(t, "POST", "/api/v1/notifications", map[string]interface{}{
		"recipient": "+1234567890",
		"channel":   "sms",
		"content":   "Already queued",
	})
	var created domain.Notification
	decodeJSON(t, resp, &created)

	// Manually mark as sent via direct DB update
	_, err := pgContainer.Pool.Exec(context.Background(),
		"UPDATE notifications SET status = 'sent' WHERE id = $1", created.ID)
	require.NoError(t, err)

	// Attempt cancel → should fail
	resp = ts.do(t, "POST", fmt.Sprintf("/api/v1/notifications/%s/cancel", created.ID), nil)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	resp.Body.Close()
}

func TestAPI_ListNotifications(t *testing.T) {
	ts := setupTestServer(t)

	// Create a few notifications
	for _, ch := range []string{"sms", "email", "push"} {
		ts.do(t, "POST", "/api/v1/notifications", map[string]interface{}{
			"recipient": "+1234567890",
			"channel":   ch,
			"content":   "Hello " + ch,
		}).Body.Close()
	}

	t.Run("list all", func(t *testing.T) {
		resp := ts.do(t, "GET", "/api/v1/notifications", nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var body map[string]interface{}
		decodeJSON(t, resp, &body)
		assert.Equal(t, float64(3), body["total"])
	})

	t.Run("filter by channel", func(t *testing.T) {
		resp := ts.do(t, "GET", "/api/v1/notifications?channel=sms", nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var body map[string]interface{}
		decodeJSON(t, resp, &body)
		assert.Equal(t, float64(1), body["total"])
	})

	t.Run("pagination", func(t *testing.T) {
		resp := ts.do(t, "GET", "/api/v1/notifications?limit=1&offset=0", nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var body map[string]interface{}
		decodeJSON(t, resp, &body)
		assert.Equal(t, float64(3), body["total"])
		notifs := body["notifications"].([]interface{})
		assert.Len(t, notifs, 1)
	})
}

// --- Batch ---

func TestAPI_BatchCreate(t *testing.T) {
	ts := setupTestServer(t)

	notifications := make([]map[string]interface{}, 3)
	for i := range notifications {
		notifications[i] = map[string]interface{}{
			"recipient": fmt.Sprintf("+12345%05d", i),
			"channel":   "sms",
			"content":   fmt.Sprintf("Message %d", i),
		}
	}

	resp := ts.do(t, "POST", "/api/v1/notifications/batch", map[string]interface{}{
		"notifications": notifications,
	})
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var body map[string]interface{}
	decodeJSON(t, resp, &body)
	assert.Equal(t, float64(3), body["count"])
	assert.NotEmpty(t, body["batch_id"])

	// Verify batch status endpoint
	batchID := body["batch_id"].(string)
	resp = ts.do(t, "GET", "/api/v1/notifications/batch/"+batchID, nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	decodeJSON(t, resp, &body)
	assert.Equal(t, float64(3), body["total"])
}

func TestAPI_BatchCreate_Empty(t *testing.T) {
	ts := setupTestServer(t)

	resp := ts.do(t, "POST", "/api/v1/notifications/batch", map[string]interface{}{
		"notifications": []map[string]interface{}{},
	})
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

// --- Templates ---

func TestAPI_TemplateCRUD(t *testing.T) {
	ts := setupTestServer(t)

	// Create
	resp := ts.do(t, "POST", "/api/v1/templates", map[string]interface{}{
		"name":      "welcome-sms",
		"channel":   "sms",
		"content":   "Hello {{.Name}}, welcome!",
		"variables": []string{"Name"},
	})
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var created domain.Template
	decodeJSON(t, resp, &created)
	assert.Equal(t, "welcome-sms", created.Name)

	// Get
	resp = ts.do(t, "GET", "/api/v1/templates/"+created.ID.String(), nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var got domain.Template
	decodeJSON(t, resp, &got)
	assert.Equal(t, created.ID, got.ID)

	// Update
	resp = ts.do(t, "PUT", "/api/v1/templates/"+created.ID.String(), map[string]interface{}{
		"content": "Hi {{.Name}}, updated!",
	})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var updated domain.Template
	decodeJSON(t, resp, &updated)
	assert.Equal(t, "Hi {{.Name}}, updated!", updated.Content)

	// List
	resp = ts.do(t, "GET", "/api/v1/templates", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var listBody map[string]interface{}
	decodeJSON(t, resp, &listBody)
	templates := listBody["templates"].([]interface{})
	assert.Len(t, templates, 1)

	// Delete
	resp = ts.do(t, "DELETE", "/api/v1/templates/"+created.ID.String(), nil)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	// Verify deleted
	resp = ts.do(t, "GET", "/api/v1/templates/"+created.ID.String(), nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func TestAPI_CreateNotification_WithTemplate(t *testing.T) {
	ts := setupTestServer(t)

	// Create template first
	resp := ts.do(t, "POST", "/api/v1/templates", map[string]interface{}{
		"name":      "otp-sms",
		"channel":   "sms",
		"content":   "Your code is {{.Code}}",
		"variables": []string{"Code"},
	})
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	var tmpl domain.Template
	decodeJSON(t, resp, &tmpl)

	// Create notification using template
	resp = ts.do(t, "POST", "/api/v1/notifications", map[string]interface{}{
		"recipient":      "+1234567890",
		"channel":        "sms",
		"template_id":    tmpl.ID.String(),
		"template_vars":  map[string]string{"Code": "1234"},
	})
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var n domain.Notification
	decodeJSON(t, resp, &n)
	assert.Equal(t, "Your code is 1234", n.Content)
}

// --- Metrics ---

func TestAPI_Metrics(t *testing.T) {
	ts := setupTestServer(t)

	resp := ts.do(t, "GET", "/metrics", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]interface{}
	decodeJSON(t, resp, &body)
	assert.Contains(t, body, "channels")
	assert.Contains(t, body, "websocket_connections")
}
