package api

import (
	"log/slog"
	"net/http"

	"github.com/insider/insider/internal/api/handler"
	"github.com/insider/insider/internal/api/middleware"
)

func NewRouter(
	logger *slog.Logger,
	notifHandler *handler.NotificationHandler,
	tmplHandler *handler.TemplateHandler,
	healthHandler *handler.HealthHandler,
	metricsHandler *handler.MetricsHandler,
	wsHandler *handler.WebSocketHandler,
) http.Handler {
	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("GET /health", healthHandler.Health)
	mux.HandleFunc("GET /ready", healthHandler.Ready)

	// Metrics
	mux.HandleFunc("GET /metrics", metricsHandler.Metrics)

	// WebSocket
	mux.HandleFunc("GET /ws/notifications", wsHandler.Upgrade)

	// Notifications
	mux.HandleFunc("POST /api/v1/notifications", notifHandler.Create)
	mux.HandleFunc("POST /api/v1/notifications/batch", notifHandler.BatchCreate)
	mux.HandleFunc("GET /api/v1/notifications/{id}", notifHandler.Get)
	mux.HandleFunc("GET /api/v1/notifications/batch/{batch_id}", notifHandler.GetBatch)
	mux.HandleFunc("POST /api/v1/notifications/{id}/cancel", notifHandler.Cancel)
	mux.HandleFunc("GET /api/v1/notifications", notifHandler.List)

	// Templates
	mux.HandleFunc("POST /api/v1/templates", tmplHandler.Create)
	mux.HandleFunc("GET /api/v1/templates/{id}", tmplHandler.Get)
	mux.HandleFunc("PUT /api/v1/templates/{id}", tmplHandler.Update)
	mux.HandleFunc("DELETE /api/v1/templates/{id}", tmplHandler.Delete)
	mux.HandleFunc("GET /api/v1/templates", tmplHandler.List)

	// Apply middleware
	var h http.Handler = mux
	h = middleware.Gzip(h)
	h = middleware.MaxBodySize(5 << 20)(h) // 5MB limit
	h = middleware.Logging(logger)(h)
	h = middleware.Recovery(logger)(h)
	h = middleware.RequestID(h)

	return h
}
