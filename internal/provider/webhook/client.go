package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/insider/insider/internal/domain"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

type webhookPayload struct {
	NotificationID string `json:"notification_id"`
	Recipient      string `json:"recipient"`
	Channel        string `json:"channel"`
	Content        string `json:"content"`
	Timestamp      string `json:"timestamp"`
}

func (c *Client) Send(ctx context.Context, n *domain.Notification) (string, error) {
	payload := webhookPayload{
		NotificationID: n.ID.String(),
		Recipient:      n.Recipient,
		Channel:        string(n.Channel),
		Content:        n.Content,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("provider returned status %d", resp.StatusCode)
	}

	// Generate a provider message ID
	providerMsgID := uuid.New().String()
	return providerMsgID, nil
}
