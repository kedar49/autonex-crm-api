package google

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type CalendarEventInput struct {
	Summary     string    `json:"summary"`
	Description string    `json:"description,omitempty"`
	StartAt     time.Time `json:"start_at"`
	EndAt       time.Time `json:"end_at"`
	Attendees   []string  `json:"attendees,omitempty"`
	// TimeZone is an IANA name sent alongside the timestamps. Google needs it to
	// render the invitation in the organiser's zone; without it a DST change can
	// shift the event by an hour.
	TimeZone string `json:"time_zone,omitempty"`
}

type CalendarEventResponse struct {
	ID       string `json:"id"`
	HtmlLink string `json:"htmlLink"`
	Status   string `json:"status"`
	// HangoutLink is the Google Meet URL. Google only populates it when the
	// request asked for a conference and conferenceDataVersion=1 was sent.
	HangoutLink    string `json:"hangoutLink"`
	ConferenceData struct {
		EntryPoints []struct {
			EntryPointType string `json:"entryPointType"`
			URI            string `json:"uri"`
		} `json:"entryPoints"`
	} `json:"conferenceData"`
}

// MeetLink returns the Meet URL, preferring the top-level field and falling back
// to the video entry point — both are documented, and which one is populated has
// varied between account types.
func (r *CalendarEventResponse) MeetLink() string {
	if r.HangoutLink != "" {
		return r.HangoutLink
	}
	for _, e := range r.ConferenceData.EntryPoints {
		if e.EntryPointType == "video" && e.URI != "" {
			return e.URI
		}
	}
	return ""
}

type Client struct {
	httpClient *http.Client
}

func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// CreateEvent books an event on the caller's primary calendar and attaches a
// Google Meet to it.
//
// doer is the authenticated transport. Passing an *http.Client built from an
// oauth2 TokenSource means an expired access token is refreshed transparently,
// which a raw bearer string cannot do.
func (c *Client) CreateEvent(ctx context.Context, doer *http.Client, input CalendarEventInput) (*CalendarEventResponse, error) {
	requestID, err := randomRequestID()
	if err != nil {
		return nil, err
	}

	start := map[string]string{"dateTime": input.StartAt.Format(time.RFC3339)}
	end := map[string]string{"dateTime": input.EndAt.Format(time.RFC3339)}
	if input.TimeZone != "" {
		start["timeZone"] = input.TimeZone
		end["timeZone"] = input.TimeZone
	}

	payload := map[string]any{
		"summary":     input.Summary,
		"description": input.Description,
		"start":       start,
		"end":         end,
		// requestId makes the conference creation idempotent: a retry with the
		// same id returns the same Meet rather than minting a second one.
		"conferenceData": map[string]any{
			"createRequest": map[string]any{
				"requestId":             requestID,
				"conferenceSolutionKey": map[string]string{"type": "hangoutsMeet"},
			},
		},
	}

	if len(input.Attendees) > 0 {
		atts := make([]map[string]string, 0, len(input.Attendees))
		for _, a := range input.Attendees {
			atts = append(atts, map[string]string{"email": a})
		}
		payload["attendees"] = atts
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	// conferenceDataVersion=1 is what makes Google honour the createRequest
	// above; without it the event is created silently without a Meet.
	const url = "https://www.googleapis.com/calendar/v3/calendars/primary/events" +
		"?conferenceDataVersion=1"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	httpClient := doer
	if httpClient == nil {
		httpClient = c.httpClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		// Google explains refusals (missing scope, Calendar API disabled) in the
		// body; without it the status alone is nearly undiagnosable.
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("google calendar: status %d: %s", resp.StatusCode, string(detail))
	}

	var res CalendarEventResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return &res, nil
}

func randomRequestID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
