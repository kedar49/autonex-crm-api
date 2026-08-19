package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type payload struct {
	Title             string     `json:"title"`
	Amount            float64    `json:"amount"`
	ExpectedCloseDate *time.Time `json:"expectedCloseDate"`
}

// The message has to name the offending field. A flat "invalid request body"
// reads identically for a typo, a bad date and a truncated payload, which is
// what made the deals date bug a repro-and-bisect job instead of a read.
func TestDecodeJSONExplainsWhatIsWrong(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "bare date into a time.Time",
			body: `{"title":"x","expectedCloseDate":"2026-09-11"}`,
			want: `invalid timestamp "2026-09-11": expected an RFC 3339 timestamp`,
		},
		{
			name: "unknown field",
			body: `{"title":"x","nope":1}`,
			want: `unknown field "nope"`,
		},
		{
			name: "string where a number belongs",
			body: `{"amount":"lots"}`,
			want: `invalid value for "amount": expected a number`,
		},
		{
			name: "truncated json",
			body: `{"title":`,
			want: "request body is incomplete",
		},
		{
			name: "malformed json",
			body: `{"title":]}`,
			want: "malformed JSON at position",
		},
		{
			name: "empty body",
			body: ``,
			want: "request body is empty",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))

			var dst payload
			if DecodeJSON(rec, req, &dst) {
				t.Fatalf("expected decode to fail for %s", tc.body)
			}
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}

			var got map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("response is not the error envelope: %v", err)
			}
			if !strings.Contains(got["error"], tc.want) {
				t.Errorf("error = %q, want it to contain %q", got["error"], tc.want)
			}
		})
	}
}

func TestDecodeJSONAcceptsAValidBody(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"title":"x","amount":10,"expectedCloseDate":"2026-09-11T00:00:00Z"}`))

	var dst payload
	if !DecodeJSON(rec, req, &dst) {
		t.Fatalf("valid body rejected: %s", rec.Body.String())
	}
	if dst.Title != "x" || dst.Amount != 10 || dst.ExpectedCloseDate == nil {
		t.Errorf("decoded = %+v, want the body's values", dst)
	}
}

// The body cap must stay a 400 with a clear reason rather than a decoder error
// nobody can act on.
func TestDecodeJSONRejectsAnOversizedBody(t *testing.T) {
	huge := `{"title":"` + strings.Repeat("a", maxBodyBytes+1) + `"}`

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(huge))

	var dst payload
	if DecodeJSON(rec, req, &dst) {
		t.Fatal("expected the oversized body to be rejected")
	}
	if !strings.Contains(rec.Body.String(), "too large") {
		t.Errorf("error = %s, want it to mention the size", rec.Body.String())
	}
}
