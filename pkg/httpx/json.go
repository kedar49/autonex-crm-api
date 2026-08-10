// Package httpx holds the small JSON request/response helpers every domain
// module's handlers need, so the error envelope stays identical across modules.
package httpx

import (
	"encoding/json"
	"log"
	"net/http"
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
		WriteError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}
