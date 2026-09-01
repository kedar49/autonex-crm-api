package middleware

import (
	"net/http"
	"strings"
)

// CORS allows the browser SPA (served from allowedOrigin) to call the API from a
// different origin.
func CORS(allowedOrigin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqOrigin := r.Header.Get("Origin")
			if isAllowedOrigin(allowedOrigin, reqOrigin) {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", reqOrigin)
				// Responses vary by Origin, so caches must not reuse them across origins.
				h.Add("Vary", "Origin")
				h.Set("Access-Control-Allow-Credentials", "true")
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				h.Set("Access-Control-Max-Age", "600")
			}

			// Short-circuit the preflight request; there is nothing to route.
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isAllowedOrigin(allowed, origin string) bool {
	if allowed == "" || origin == "" {
		return false
	}
	allowed = strings.TrimRight(allowed, "/")
	origin = strings.TrimRight(origin, "/")
	if origin == allowed {
		return true
	}
	// Support localhost / 127.0.0.1 dev variations
	if (strings.HasPrefix(allowed, "http://localhost:") || strings.HasPrefix(allowed, "http://127.0.0.1:")) &&
		(strings.HasPrefix(origin, "http://localhost:") || strings.HasPrefix(origin, "http://127.0.0.1:")) {
		return true
	}
	return false
}
