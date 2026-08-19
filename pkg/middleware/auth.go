package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"

	"github.com/go-crm/services/pkg/httpx"
)

type ctxKey string

const (
	userIDKey ctxKey = "userID"
	orgIDKey  ctxKey = "orgID"
)

// RequireJWT validates a Bearer token and injects the subject and organization
// into the request context.
//
// A token without an "org" claim is rejected: every CRM query is org-scoped, so
// a request that cannot name its tenant has no business reaching a handler.
// (Tokens issued before tenancy existed fall into this case and force one
// re-login.)
func RequireJWT(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if raw == "" {
				raw = r.URL.Query().Get("token")
			}
			if raw == "" {
				httpx.WriteError(w, http.StatusUnauthorized, "missing token")
				return
			}

			token, err := jwt.Parse(raw, func(t *jwt.Token) (interface{}, error) {
				// Enforce HMAC to prevent alg-confusion (e.g. "none" / RS256) attacks.
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
				}
				return []byte(secret), nil
			}, jwt.WithValidMethods([]string{"HS256"}))
			if err != nil || !token.Valid {
				httpx.WriteError(w, http.StatusUnauthorized, "invalid token")
				return
			}

			sub, _ := token.Claims.GetSubject()
			claims, _ := token.Claims.(jwt.MapClaims)
			org, _ := claims["org"].(string)
			if sub == "" || org == "" {
				httpx.WriteError(w, http.StatusUnauthorized, "invalid token")
				return
			}

			ctx := context.WithValue(r.Context(), userIDKey, sub)
			ctx = context.WithValue(ctx, orgIDKey, org)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserID extracts the authenticated user id from the request context.
func UserID(ctx context.Context) string {
	id, _ := ctx.Value(userIDKey).(string)
	return id
}

// OrgID extracts the authenticated user's organization (tenant) id from the
// request context. Handlers must scope every query by it.
func OrgID(ctx context.Context) string {
	id, _ := ctx.Value(orgIDKey).(string)
	return id
}
