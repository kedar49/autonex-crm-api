// Package httpx holds the small JSON request/response helpers every domain
// module's handlers need, so the error envelope stays identical across modules.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"reflect"
	"strings"
	"time"
)

// maxBodyBytes caps a decoded request body (64 KiB) — plenty for CRM records and
// a cheap guard against a client streaming an unbounded body at us.
const maxBodyBytes = 1 << 16

// WriteJSON writes v as JSON with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes the {"error": msg} envelope the clients expect.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}

// WriteServerError returns the opaque 500 message to the client *and logs the
// cause*.
//
// Clients must never see an internal error — it leaks schema and query shapes —
// but a 500 that leaves no trace anywhere is undebuggable. Every module's
// fallback branch goes through here.
func WriteServerError(w http.ResponseWriter, msg string, err error) {
	log.Printf("500 %s: %v", msg, err)
	WriteError(w, http.StatusInternalServerError, msg)
}

// DecodeJSON reads a size-limited JSON body into dst. On failure it has already
// written a 400 response, so the handler just returns.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		WriteError(w, http.StatusBadRequest, decodeMessage(err))
		return false
	}
	return true
}

// decodeMessage turns a decoder error into something the caller can act on.
//
// A flat "invalid request body" is the same answer for an unknown field, a
// mistyped value and a truncated payload, which makes a client bug a guessing
// game — a bare "2026-09-11" sent to a time.Time field cost an afternoon exactly
// once. Everything described here is the *client's own* payload, so naming the
// field leaks nothing about the server.
func decodeMessage(err error) string {
	var typeErr *json.UnmarshalTypeError
	var syntaxErr *json.SyntaxError
	var maxErr *http.MaxBytesError
	var timeErr *time.ParseError

	switch {
	case errors.Is(err, io.EOF):
		return "request body is empty"

	case errors.Is(err, io.ErrUnexpectedEOF):
		return "request body is incomplete"

	case errors.As(err, &maxErr):
		return "request body is too large"

	case errors.As(err, &syntaxErr):
		return fmt.Sprintf("malformed JSON at position %d", syntaxErr.Offset)

	// time.Time has its own UnmarshalJSON, so a bad date never reaches the
	// UnmarshalTypeError branch below and arrives with no field name attached.
	// Quoting the value that failed is the next best handle on it.
	case errors.As(err, &timeErr):
		return fmt.Sprintf("invalid timestamp %q: expected %s",
			strings.Trim(timeErr.Value, `"`), expectation(reflect.TypeOf(time.Time{})))

	case errors.As(err, &typeErr):
		if typeErr.Field == "" {
			return fmt.Sprintf("invalid request body: expected %s", expectation(typeErr.Type))
		}
		return fmt.Sprintf("invalid value for %q: expected %s", typeErr.Field, expectation(typeErr.Type))

	// DisallowUnknownFields reports this as a plain error with no dedicated type,
	// so the prefix is the only handle on it.
	case strings.HasPrefix(err.Error(), unknownFieldPrefix):
		return "unknown field " + strings.TrimPrefix(err.Error(), unknownFieldPrefix)

	default:
		return "invalid request body"
	}
}

const unknownFieldPrefix = "json: unknown field "

// expectation describes a Go type the way a client author would need to hear it.
func expectation(t reflect.Type) string {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil {
		return "a different type"
	}

	if t == reflect.TypeOf(time.Time{}) {
		return "an RFC 3339 timestamp, e.g. 2026-09-11T00:00:00Z"
	}

	switch t.Kind() {
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "true or false"
	case reflect.Float32, reflect.Float64,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "a number"
	case reflect.Slice, reflect.Array:
		return "an array"
	case reflect.Struct, reflect.Map:
		return "an object"
	default:
		return t.String()
	}
}
