package middleware

import "net/http"

// CORS allows the browser SPA (served from allowedOrigin) to call the API from
// a different origin. The API authenticates via a Bearer token in the
// Authorization header (not cookies), so credentials are not enabled and a
// single, explicit origin is echoed rather than "*".
//
// An empty allowedOrigin disables CORS (no headers added) — useful when the API
// and web app share an origin behind a reverse proxy.
func CORS(allowedOrigin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if allowedOrigin != "" && r.Header.Get("Origin") == allowedOrigin {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", allowedOrigin)
				// Responses vary by Origin, so caches must not reuse them across origins.
				h.Add("Vary", "Origin")
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
