package websocket

import (
	"context"
	"time"

	"github.com/coder/websocket"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	sendBuffer = 256
)

type Client struct {
	conn *websocket.Conn
	hub  *Hub
	send chan []byte
	ctx  context.Context
}

func NewClient(conn *websocket.Conn, hub *Hub, ctx context.Context) *Client {
	return &Client{
		conn: conn,
		hub:  hub,
		send: make(chan []byte, sendBuffer),
		ctx:  ctx,
	}
}

func (c *Client) ReadPump() {
	defer func() {
		c.hub.Unregister(c)
		c.conn.CloseNow()
	}()

	for {
		_, _, err := c.conn.Read(c.ctx)
		if err != nil {
			return
		}
		// We don't process incoming messages; clients are read-only subscribers
	}
}

func (c *Client) WritePump() {
	defer c.conn.CloseNow()

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
