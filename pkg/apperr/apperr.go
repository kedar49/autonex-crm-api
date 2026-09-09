// Package apperr holds the error kinds every domain module needs to
// distinguish, independent of how they are eventually reported.
//
// It deliberately does not import net/http. A service validating its input
// should not have to know that the answer becomes a 400 — only the handler
// layer decides that. Keeping this package transport-free is what lets the
// same service run behind an HTTP handler, a worker or a test with no
// translation shim.
package apperr

import (
	"errors"
	"fmt"
)

// Validation is a caller's-fault error: the request was understood and
// rejected, so the message is safe to show the client verbatim.
//
// This is the whole reason the type exists. Every other error a service can
// return describes something the caller cannot fix and must not see — a failed
// query leaks schema, a driver error leaks topology — so handlers answer those
// with an opaque 500. A Validation says the opposite: the text is *for* the
// caller, and hiding it would be the bug.
type Validation struct{ msg string }

func (e Validation) Error() string { return e.msg }

// Invalid builds a Validation from a format string.
//
// The message is read by whoever made the request, so write it as advice
// ("a quote needs at least one line") rather than as a field report
// ("items: len 0").
func Invalid(format string, args ...any) error {
	return Validation{msg: fmt.Sprintf(format, args...)}
}

// IsValidation reports whether err is, or wraps, a Validation.
func IsValidation(err error) bool {
	var v Validation
	return errors.As(err, &v)
}
