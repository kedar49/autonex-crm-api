package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type CalendarEventInput struct {
	Summary     string    `json:"summary"`
	Description string    `json:"description,omitempty"`
	StartAt     time.Time `json:"start_at"`
	EndAt       time.Time `json:"end_at"`
	Attendees   []string  `json:"attendees,omitempty"`
}

type CalendarEventResponse struct {
	ID       string `json:"id"`
	HtmlLink string `json:"htmlLink"`
	Status   string `json:"status"`
}

type Client struct {
	httpClient *http.Client
}

func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) CreateEvent(ctx context.Context, accessToken string, input CalendarEventInput) (*CalendarEventResponse, error) {
	payload := map[string]interface{}{
		"summary":     input.Summary,
		"description": input.Description,
		"start": map[string]string{
			"dateTime": input.StartAt.Format(time.RFC3339),
		},
		"end": map[string]string{
			"dateTime": input.EndAt.Format(time.RFC3339),
		},
	}

	if len(input.Attendees) > 0 {
		var atts []map[string]string
		for _, a := range input.Attendees {
			atts = append(atts, map[string]string{"email": a})
		}
		payload["attendees"] = atts
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://www.googleapis.com/calendar/v3/calendars/primary/events", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("google calendar error: status %d", resp.StatusCode)
	}

	var res CalendarEventResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}

	return &res, nil
}
