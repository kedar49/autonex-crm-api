package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-crm/services/pkg/apperr"
)

var (
	errMissing  = errors.New("not found")
	errConflict = errors.New("in use")
)

func rules() []Rule {
	return []Rule{
		{Err: errMissing, Status: http.StatusNotFound, Message: "widget not found"},
		{Err: errConflict, Status: http.StatusConflict, Message: "unlink it first"},
	}
}

func call(t *testing.T, err error) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	WriteDomainError(w, err, "could not load the widget", rules()...)

	var body struct {
		Error string `json:"error"`
	}
	if decErr := json.NewDecoder(w.Body).Decode(&body); decErr != nil {
		t.Fatalf("response was not the {\"error\":...} envelope: %v", decErr)
	}
	return w.Code, body.Error
}

func TestMatchesARule(t *testing.T) {
	if status, msg := call(t, errMissing); status != http.StatusNotFound || msg != "widget not found" {
		t.Errorf("got (%d, %q), want (404, \"widget not found\")", status, msg)
	}
	if status, msg := call(t, errConflict); status != http.StatusConflict || msg != "unlink it first" {
		t.Errorf("got (%d, %q), want (409, \"unlink it first\")", status, msg)
	}
}

func TestMatchesAWrappedSentinel(t *testing.T) {
	// Services add context on the way out, so the rule has to survive wrapping.
	status, msg := call(t, fmt.Errorf("delete widget 7: %w", errMissing))
	if status != http.StatusNotFound || msg != "widget not found" {
		t.Errorf("got (%d, %q), want (404, \"widget not found\")", status, msg)
	}
}

func TestValidationBecomesA400CarryingItsOwnMessage(t *testing.T) {
	// The message was written for the caller; the fallback must not replace it.
	status, msg := call(t, apperr.Invalid("name cannot be empty"))
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
	if msg != "name cannot be empty" {
		t.Errorf("message = %q, want the validation text", msg)
	}
}

func TestUnknownErrorBecomesAnOpaque500(t *testing.T) {
	// A query error names tables and columns. The client gets the fallback.
	leaky := errors.New(`pq: column "password_hash" does not exist`)
	status, msg := call(t, leaky)
	if status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", status)
	}
	if msg != "could not load the widget" {
		t.Errorf("message = %q, want the fallback", msg)
	}
	if msg == leaky.Error() {
		t.Error("the underlying error reached the client")
	}
}

func TestFirstMatchingRuleWins(t *testing.T) {
	// A specific error listed ahead of the general one it wraps must win.
	specific := fmt.Errorf("archived: %w", errConflict)
	w := httptest.NewRecorder()
	WriteDomainError(w, specific, "fallback",
		Rule{Err: specific, Status: http.StatusGone, Message: "archived"},
		Rule{Err: errConflict, Status: http.StatusConflict, Message: "unlink it first"},
	)
	if w.Code != http.StatusGone {
		t.Errorf("status = %d, want 410 from the earlier rule", w.Code)
	}
}

func TestNoRulesStillAppliesTheFallbacks(t *testing.T) {
	// integrations has a single rule and no validation errors at all; a module
	// with none must still get the 400/500 policy.
	w := httptest.NewRecorder()
	WriteDomainError(w, apperr.Invalid("bad input"), "fallback")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}
