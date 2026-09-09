package httpx

import (
	"errors"
	"net/http"

	"github.com/go-crm/services/pkg/apperr"
)

// Rule maps one of a module's sentinel errors to the status and message the
// client should get for it.
//
// Message is written out verbatim, so it must describe the caller's situation
// and nothing about ours. These strings are the module's public vocabulary —
// "unlink its contacts and deals before deleting this account" is more useful
// than "conflict" — which is why each module supplies its own rather than
// sharing a generic set.
type Rule struct {
	Err     error
	Status  int
	Message string
}

// WriteDomainError answers err using the first matching rule, and falls back to
// the two answers every module needs.
//
// Rules are tried in order with errors.Is, so a wrapped sentinel still matches
// and a more specific error listed first wins over a general one it wraps.
//
// The two fallbacks are the reason this is shared rather than left as a switch
// in every handler:
//
//   - An apperr.Validation becomes a 400 carrying its own message. That message
//     was written for the caller, so suppressing it would be the bug.
//   - Anything else becomes an opaque 500 and is logged. A query error names
//     tables and columns, so the client gets the fallback text and the detail
//     goes to the log.
//
// Nine handlers each restated both of those, which meant a tenth could restate
// neither: forget the validation case and every rejected input silently becomes
// a 500 that says "something went wrong" about a typo in an email address.
func WriteDomainError(w http.ResponseWriter, err error, fallback string, rules ...Rule) {
	for _, rule := range rules {
		if errors.Is(err, rule.Err) {
			WriteError(w, rule.Status, rule.Message)
			return
		}
	}
	if apperr.IsValidation(err) {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	WriteServerError(w, fallback, err)
}
