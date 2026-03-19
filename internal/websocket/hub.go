package websocket

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/coder/websocket"
)

type Event struct {
	Type           string `json:"type"`
	NotificationID string `json:"notification_id"`
	Channel        string `json:"channel"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
}

type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]struct{}
	logger  *slog.Logger
}

func NewHub(logger *slog.Logger) *Hub {
	return &Hub{
		clients: make(map[*Client]struct{}),
		logger:  logger,
	}
}

func (h *Hub) Register(conn *websocket.Conn, ctx context.Context) {
	client := NewClient(conn, h, ctx)
	h.mu.Lock()
	h.clients[client] = struct{}{}
	h.mu.Unlock()

	h.logger.Info("websocket client connected", "total", h.Len())

	go client.WritePump()
	go client.ReadPump()
}

func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	h.logger.Info("websocket client disconnected", "total", h.Len())
}

func (h *Hub) Broadcast(event Event) {
	data, err := json.Marshal(event)
	if err != nil {
		h.logger.Error("marshal websocket event", "error", err)
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.clients {
		if !client.WantsEvent(event.Channel) {
			continue
		}
		select {
		case client.send <- data:
		default:
			// Buffer full, skip
		}
	}
}

func (h *Hub) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
