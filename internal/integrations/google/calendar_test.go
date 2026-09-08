package google

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// capture stands in for the Google Calendar API and records what we sent.
type capture struct {
	query url.Values
	body  map[string]any
}

func serve(t *testing.T, status int, response string) (*Client, *capture) {
	t.Helper()
	got := &capture{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.query = r.URL.Query()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)

	c := NewClient()
	c.baseURL = srv.URL
	return c, got
}

func input() CalendarEventInput {
	start := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	return CalendarEventInput{
		Summary:  "Call with Acme",
		StartAt:  start,
		EndAt:    start.Add(30 * time.Minute),
		TimeZone: "Asia/Kolkata",
	}
}

const okResponse = `{"id":"evt1","htmlLink":"https://cal","hangoutLink":"https://meet.google.com/abc-defg-hij"}`

// The whole point of the feature: without sendUpdates Google attaches the
// attendee to the event and never emails them, so the invite silently is not an
// invite.
func TestAttendeesAreEmailedWithSendUpdatesAll(t *testing.T) {
	c, got := serve(t, 200, okResponse)

	in := input()
	in.Attendees = []string{"lead@example.com", "contact@example.com"}

	if _, err := c.CreateEvent(context.Background(), nil, in); err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}

	if got.query.Get("sendUpdates") != "all" {
		t.Errorf("sendUpdates = %q, want \"all\" — attendees would not be emailed",
			got.query.Get("sendUpdates"))
	}
	if got.query.Get("conferenceDataVersion") != "1" {
		t.Errorf("conferenceDataVersion = %q, want \"1\" — no Meet link would be created",
			got.query.Get("conferenceDataVersion"))
	}

	atts, _ := got.body["attendees"].([]any)
	if len(atts) != 2 {
		t.Fatalf("attendees = %v, want 2 entries", got.body["attendees"])
	}
	first, _ := atts[0].(map[string]any)
	if first["email"] != "lead@example.com" {
		t.Errorf("first attendee = %v, want lead@example.com", first["email"])
	}
}

// A meeting with nobody invited must not ask Google to send mail, so an internal
// booking stays internal.
func TestNoAttendeesMeansNoInvitesSent(t *testing.T) {
	c, got := serve(t, 200, okResponse)

	if _, err := c.CreateEvent(context.Background(), nil, input()); err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}

	if s := got.query.Get("sendUpdates"); s != "" && s != "none" {
		t.Errorf("sendUpdates = %q, want none or absent for an empty attendee list", s)
	}
	if _, present := got.body["attendees"]; present {
		t.Errorf("attendees key present with no attendees: %v", got.body["attendees"])
	}
}

func TestMeetLinkPrefersHangoutLink(t *testing.T) {
	c, _ := serve(t, 200, okResponse)
	res, err := c.CreateEvent(context.Background(), nil, input())
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if res.MeetLink() != "https://meet.google.com/abc-defg-hij" {
		t.Errorf("MeetLink = %q", res.MeetLink())
	}
}

// Some account types populate only the conference entry points.
func TestMeetLinkFallsBackToVideoEntryPoint(t *testing.T) {
	const body = `{"id":"evt2","conferenceData":{"entryPoints":[
		{"entryPointType":"phone","uri":"tel:+1"},
		{"entryPointType":"video","uri":"https://meet.google.com/xyz"}]}}`
	c, _ := serve(t, 200, body)

	res, err := c.CreateEvent(context.Background(), nil, input())
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if res.MeetLink() != "https://meet.google.com/xyz" {
		t.Errorf("MeetLink = %q, want the video entry point", res.MeetLink())
	}
}

// Google explains refusals in the body; the status alone is nearly undiagnosable.
func TestErrorBodyIsSurfaced(t *testing.T) {
	c, _ := serve(t, 403, `{"error":{"message":"Calendar API has not been used"}}`)

	_, err := c.CreateEvent(context.Background(), nil, input())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "Calendar API has not been used") {
		t.Errorf("err = %v, want it to carry the API explanation", err)
	}
}
