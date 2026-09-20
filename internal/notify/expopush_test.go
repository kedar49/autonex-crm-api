package notify

import (
	"encoding/json"
	"testing"
)

func TestValidExpoToken(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  bool
	}{
		{"modern form", "ExponentPushToken[xxxxxxxxxxxxxxxxxxxxxx]", true},
		{"legacy form", "ExpoPushToken[xxxxxxxxxxxxxxxxxxxxxx]", true},
		{"empty", "", false},
		{"no wrapper", "xxxxxxxxxxxxxxxxxxxxxx", false},
		{"missing bracket", "ExponentPushToken[xxxxxxxxxxxx", false},
		// An FCM token is what you get by wiring the app up the heavy way; it is
		// not something Expo's service will accept, so it must be rejected at
		// registration rather than failing silently on every send.
		{"raw fcm token", "fGf1...:APA91bH_long_fcm_token", false},
		{"prefix only", "ExponentPushToken[]", false},
		{"absurdly long", "ExponentPushToken[" + string(make([]byte, 300)) + "]", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidExpoToken(tc.token); got != tc.want {
				t.Errorf("ValidExpoToken(%q) = %v, want %v", tc.token, got, tc.want)
			}
		})
	}
}

// The app routes a notification tap from the data payload alone, without a
// round-trip, so the field names are a contract with the client.
func TestExpoMessageCarriesRoutingData(t *testing.T) {
	// Arrange
	badge := 3
	msg := expoMessage{
		To:        "ExponentPushToken[abc]",
		Title:     "Deal moved",
		Body:      "Tata Steel · Perimeter VIGIL moved to Won",
		Sound:     "default",
		Badge:     &badge,
		ChannelID: "default",
		Priority:  "high",
		Data: map[string]any{
			"notificationId": "n-1",
			"type":           "deal_moved",
			"actionUrl":      "/deals",
			"priority":       "success",
		},
	}

	// Act
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Assert
	if out["to"] != "ExponentPushToken[abc]" {
		t.Errorf("to = %v", out["to"])
	}
	if out["badge"] != float64(3) {
		t.Errorf("badge = %v, want 3", out["badge"])
	}
	if out["channelId"] != "default" {
		t.Errorf("channelId = %v — Android needs it for heads-up display", out["channelId"])
	}
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("data missing or not an object: %v", out["data"])
	}
	for _, key := range []string{"notificationId", "type", "actionUrl", "priority"} {
		if _, present := data[key]; !present {
			t.Errorf("data.%s missing — the app cannot route the tap without it", key)
		}
	}
}

// A zero badge must still be sent, so clearing the last notification on the web
// clears the icon badge on the phone. That is why Badge is a pointer: omitempty
// on a plain int would drop exactly the value that means "clear it".
func TestZeroBadgeIsSent(t *testing.T) {
	zero := 0
	raw, err := json.Marshal(expoMessage{To: "ExponentPushToken[abc]", Badge: &zero})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := out["badge"]; !present {
		t.Error("badge omitted when zero — the phone would keep a stale count")
	}
}

// Expo rejects a request carrying more than 100 messages, so a user with many
// devices must be split across requests.
func TestBatchSizeRespectsExpoLimit(t *testing.T) {
	if expoBatchSize > 100 {
		t.Errorf("expoBatchSize = %d, Expo accepts at most 100 per request", expoBatchSize)
	}

	total := 250
	batches := 0
	for start := 0; start < total; start += expoBatchSize {
		end := min(start+expoBatchSize, total)
		if end-start > 100 {
			t.Fatalf("batch of %d exceeds the limit", end-start)
		}
		batches++
	}
	if batches != 3 {
		t.Errorf("split %d messages into %d batches, want 3", total, batches)
	}
}

// DeviceNotRegistered is the one ticket error that must prune the token; every
// other error is transient and the row should survive it.
func TestExpoResponseDecodesDeviceNotRegistered(t *testing.T) {
	body := `{"data":[
		{"status":"ok","id":"t-1"},
		{"status":"error","message":"\"ExponentPushToken[x]\" is not a registered push notification recipient","details":{"error":"DeviceNotRegistered"}},
		{"status":"error","message":"rate limited","details":{"error":"MessageRateExceeded"}}
	]}`

	var parsed expoResponse
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(parsed.Data) != 3 {
		t.Fatalf("got %d tickets, want 3", len(parsed.Data))
	}
	if parsed.Data[0].Status != "ok" {
		t.Errorf("ticket 0 status = %q", parsed.Data[0].Status)
	}
	if parsed.Data[1].Details.Error != "DeviceNotRegistered" {
		t.Errorf("ticket 1 error = %q — the token would never be pruned",
			parsed.Data[1].Details.Error)
	}
	if parsed.Data[2].Details.Error == "DeviceNotRegistered" {
		t.Error("a rate-limit ticket must not be read as a dead device")
	}
}
