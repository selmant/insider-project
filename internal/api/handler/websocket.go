package handler

import (
	"log/slog"
	"net/http"

	"github.com/coder/websocket"

	ws "github.com/insider/insider/internal/websocket"
)

type WebSocketHandler struct {
	hub    *ws.Hub
	logger *slog.Logger
}

func NewWebSocketHandler(hub *ws.Hub, logger *slog.Logger) *WebSocketHandler {
	return &WebSocketHandler{hub: hub, logger: logger}
}

func (h *WebSocketHandler) Upgrade(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		h.logger.Error("websocket upgrade failed", "error", err)
		return
	}

	h.hub.Register(conn, r.Context())
}
