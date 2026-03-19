package websocket

import (
	"context"
	"encoding/json"
	"time"

	"github.com/coder/websocket"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	sendBuffer = 256
)

// SubscribeMessage is sent by clients to filter events.
type SubscribeMessage struct {
	Type     string   `json:"type"`
	Channels []string `json:"channels,omitempty"`
}

type Client struct {
	conn     *websocket.Conn
	hub      *Hub
	send     chan []byte
	ctx      context.Context
	channels map[string]struct{} // subscribed channels; empty = all
}

func NewClient(conn *websocket.Conn, hub *Hub, ctx context.Context) *Client {
	return &Client{
		conn:     conn,
		hub:      hub,
		send:     make(chan []byte, sendBuffer),
		ctx:      ctx,
		channels: make(map[string]struct{}),
	}
}

func (c *Client) ReadPump() {
	defer func() {
		c.hub.Unregister(c)
		_ = c.conn.CloseNow()
	}()

	for {
		_, data, err := c.conn.Read(c.ctx)
		if err != nil {
			return
		}

		var msg SubscribeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		if msg.Type == "subscribe" {
			c.hub.mu.Lock()
			c.channels = make(map[string]struct{})
			for _, ch := range msg.Channels {
				c.channels[ch] = struct{}{}
			}
			c.hub.mu.Unlock()
		}
	}
}

func (c *Client) WritePump() {
	defer func() { _ = c.conn.CloseNow() }()

	for {
		select {
		case <-c.ctx.Done():
			return
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			ctx, cancel := context.WithTimeout(c.ctx, writeWait)
			err := c.conn.Write(ctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// WantsEvent returns true if the client should receive events for the given channel.
func (c *Client) WantsEvent(channel string) bool {
	if len(c.channels) == 0 {
		return true // no filter = receive all
	}
	_, ok := c.channels[channel]
	return ok
}
