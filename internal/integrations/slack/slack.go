package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Notification struct {
	ChannelID string  `json:"channel,omitempty"`
	Text      string  `json:"text"`
	Blocks    []Block `json:"blocks,omitempty"`
}

type Block struct {
	Type string     `json:"type"`
	Text *TextBlock `json:"text,omitempty"`
}

type TextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type Client struct {
	httpClient *http.Client
}

func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) SendWebhook(ctx context.Context, webhookURL string, notif Notification) error {
	body, err := json.Marshal(notif)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("slack webhook error: status %d", resp.StatusCode)
	}

	return nil
}
