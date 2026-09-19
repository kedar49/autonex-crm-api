package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// Delivery of notifications to the mobile app, through Expo's push service.
//
// Expo rather than APNs and FCM directly: the app is an Expo managed build, so
// the credentials live with EAS and this service needs no certificate, no
// service-account key and no per-platform payload. One authenticated POST
// reaches both stores. The cost is a third party in the path, which is why a
// failure here is logged and swallowed — see Push.

const (
	expoPushURL = "https://exp.host/--/api/v2/push/send"

	// Expo accepts at most 100 messages per request.
	expoBatchSize = 100

	expoTimeout = 10 * time.Second
)

// expoMessage is one push, as Expo's API expects it.
type expoMessage struct {
	To    string         `json:"to"`
	Title string         `json:"title"`
	Body  string         `json:"body"`
	Sound string         `json:"sound,omitempty"`
	Data  map[string]any `json:"data,omitempty"`
	// Badge is the iOS app-icon count. Android ignores it.
	Badge *int `json:"badge,omitempty"`
	// ChannelID selects the Android notification channel the app registered,
	// which is what decides heads-up display and sound on Android 8+.
	ChannelID string `json:"channelId,omitempty"`
	Priority  string `json:"priority,omitempty"`
}

// expoResponse is the subset of Expo's reply we act on. Every message gets a
// ticket; a ticket with status "error" and a DeviceNotRegistered code means the
// app was uninstalled and the token should be dropped.
type expoResponse struct {
	Data []struct {
		Status  string `json:"status"`
		ID      string `json:"id"`
		Message string `json:"message"`
		Details struct {
			Error string `json:"error"`
		} `json:"details"`
	} `json:"data"`
	Errors []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

// Pusher delivers notifications to a user's registered devices.
type Pusher struct {
	store  *Store
	client *http.Client
	// accessToken is optional. Expo only requires it when the project has
	// enhanced security enabled, so an empty value is the normal case.
	accessToken string
}

// NewPusher builds a Pusher over the notification store.
func NewPusher(store *Store, accessToken string) *Pusher {
	return &Pusher{
		store:       store,
		client:      &http.Client{Timeout: expoTimeout},
		accessToken: accessToken,
	}
}

// Push delivers one already-recorded notification to every device the user has
// registered.
//
// Never returns an error. A notification is durable the moment it is in the
// database and the app can list it; the push is a best-effort nudge on top. A
// dead third party must not fail the deal move that triggered it, so everything
// here is logged and swallowed.
func (p *Pusher) Push(ctx context.Context, item NotificationItem, badge int) {
	if p == nil || p.store == nil {
		return
	}

	tokens, err := p.store.DeviceTokens(ctx, item.UserID)
	if err != nil {
		log.Printf("notify: could not load device tokens for %s: %v", item.UserID, err)
		return
	}
	if len(tokens) == 0 {
		return // nobody has the app installed; nothing to do
	}

	messages := make([]expoMessage, 0, len(tokens))
	for _, t := range tokens {
		messages = append(messages, expoMessage{
			To:    t,
			Title: item.Title,
			Body:  item.Body,
			Sound: "default",
			Badge: &badge,
			// Matches the channel the app creates on launch.
			ChannelID: "default",
			Priority:  "high",
			// What the app needs to route the tap without another round-trip.
			Data: map[string]any{
				"notificationId": item.ID,
				"type":           item.Type,
				"actionUrl":      item.ActionURL,
				"priority":       item.Priority,
			},
		})
	}

	for start := 0; start < len(messages); start += expoBatchSize {
		end := min(start+expoBatchSize, len(messages))
		p.send(ctx, messages[start:end])
	}
}

// send posts one batch and prunes tokens Expo reports as dead.
func (p *Pusher) send(ctx context.Context, batch []expoMessage) {
	body, err := json.Marshal(batch)
	if err != nil {
		log.Printf("notify: could not encode push batch: %v", err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, expoPushURL, bytes.NewReader(body))
	if err != nil {
		log.Printf("notify: could not build push request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if p.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.accessToken)
	}

	res, err := p.client.Do(req)
	if err != nil {
		log.Printf("notify: push request failed: %v", err)
		return
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		log.Printf("notify: expo returned %d for a batch of %d", res.StatusCode, len(batch))
		return
	}

	var parsed expoResponse
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		log.Printf("notify: could not decode expo response: %v", err)
		return
	}
	for _, e := range parsed.Errors {
		log.Printf("notify: expo error %s: %s", e.Code, e.Message)
	}

	// Tickets come back in request order, so index i answers batch[i].
	for i, ticket := range parsed.Data {
		if ticket.Status != "error" || i >= len(batch) {
			continue
		}
		if ticket.Details.Error == "DeviceNotRegistered" {
			// The app was uninstalled or the token was reissued. Keeping the row
			// would mean sending into the void on every notification forever.
			if err := p.store.DeleteDeviceToken(ctx, batch[i].To); err != nil {
				log.Printf("notify: could not drop dead token: %v", err)
			}
			continue
		}
		log.Printf("notify: push rejected (%s): %s", ticket.Details.Error, ticket.Message)
	}
}

// ValidExpoToken reports whether a string looks like a token Expo will accept.
//
// Checked before storing rather than on send: a malformed token is a client bug
// worth a 400 at the point it happens, not a mystery error in a log days later.
func ValidExpoToken(token string) bool {
	return (hasPrefixSuffix(token, "ExponentPushToken[", "]") ||
		hasPrefixSuffix(token, "ExpoPushToken[", "]")) && len(token) < 256
}

func hasPrefixSuffix(s, prefix, suffix string) bool {
	return len(s) > len(prefix)+len(suffix) &&
		s[:len(prefix)] == prefix &&
		s[len(s)-len(suffix):] == suffix
}
