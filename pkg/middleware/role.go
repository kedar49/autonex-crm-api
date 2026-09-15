package middleware

import (
	"net/http"

	"github.com/go-crm/services/pkg/httpx"
)

// RequireRole rejects a request whose token's role claim is not one of
// allowed. It must sit after RequireJWT, which is what populates the role in
// context; a missing role (old token, or a session minted without one) is
// never in allowed, so it fails closed rather than open.
func RequireRole(allowed ...string) func(http.Handler) http.Handler {
	set := make(map[string]bool, len(allowed))
	for _, r := range allowed {
		set[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !set[Role(r.Context())] {
				httpx.WriteError(w, http.StatusForbidden, "you don't have permission to do that")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
