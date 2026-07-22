package auth

import (
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/go-crm/services/pkg/config"
)

// issueAccessToken mints a short-lived, stateless HS256 access token whose
// subject is the user id. The gateway's RequireJWT middleware validates it.
func issueAccessToken(cfg config.Config, userID, email string) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":   userID,
		"email": email,
		"iss":   cfg.JWTIssuer,
		"iat":   now.Unix(),
		"exp":   now.Add(cfg.JWTAccessTTL).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(cfg.JWTSecret))
}
