package auth

import (
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/go-crm/services/pkg/config"
)

// issueAccessToken mints a short-lived, stateless HS256 access token whose
// subject is the user id. The gateway's RequireJWT middleware validates it.
//
// The user's organization travels in the token as the "org" claim so that
// scoping a query costs no extra round-trip to the users table. The trade-off:
// a change of organization only takes effect on the next token, which is bounded
// by JWT_ACCESS_TTL (15m by default). Acceptable while a user has exactly one
// org; revisit if org switching or invitations arrive.
func issueAccessToken(cfg config.Config, u User) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":   u.ID,
		"email": u.Email,
		"org":   u.OrgID,
		"role":  u.Role,
		"iss":   cfg.JWTIssuer,
		"iat":   now.Unix(),
		"exp":   now.Add(cfg.JWTAccessTTL).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(cfg.JWTSecret))
}
