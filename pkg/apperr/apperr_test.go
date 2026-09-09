package apperr

import (
	"errors"
	"fmt"
	"testing"
)

func TestInvalidCarriesTheFormattedMessage(t *testing.T) {
	err := Invalid("a quote needs at least one line, got %d", 0)
	want := "a quote needs at least one line, got 0"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}

func TestIsValidation(t *testing.T) {
	if !IsValidation(Invalid("bad")) {
		t.Error("did not recognize its own Validation")
	}
	// Handlers branch on this to choose 400 over an opaque 500, so a plain
	// error must never be mistaken for caller-safe input feedback.
	if IsValidation(errors.New("connection reset")) {
		t.Error("treated an arbitrary error as a validation error")
	}
	if IsValidation(nil) {
		t.Error("treated nil as a validation error")
	}
}

func TestIsValidationUnwraps(t *testing.T) {
	// Services add context on the way out; the kind has to survive that.
	wrapped := fmt.Errorf("create quote: %w", Invalid("no lines"))
	if !IsValidation(wrapped) {
		t.Error("did not unwrap a wrapped Validation")
	}
	if got, want := errors.Unwrap(wrapped).Error(), "no lines"; got != want {
		t.Errorf("unwrapped message = %q, want %q", got, want)
	}
}
