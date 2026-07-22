package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type ctxKey string

const userIDKey ctxKey = "userID"

// RequireJWT validates a Bearer token and injects the subject into the request context.
func RequireJWT(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if raw == "" {
				http.Error(w, "missing token", http.StatusUnauthorized)
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
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}

			sub, _ := token.Claims.GetSubject()
			ctx := context.WithValue(r.Context(), userIDKey, sub)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserID extracts the authenticated user id from the request context.
func UserID(ctx context.Context) string {
	id, _ := ctx.Value(userIDKey).(string)
	return id
}
