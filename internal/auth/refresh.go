package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/go-crm/services/pkg/config"
)

// ErrInvalidRefresh covers an unknown, expired, revoked or replayed refresh
// token. Deliberately one error: telling a caller *which* would help an attacker
// probe for live sessions.
var ErrInvalidRefresh = errors.New("refresh token is invalid or has expired")

// RefreshCookieName is the cookie the browser holds the refresh token in.
const RefreshCookieName = "gocrm_refresh"

// refreshCookiePath scopes the cookie to the auth routes, so it is never sent on
// an ordinary API call — only refresh and logout can see it.
const refreshCookiePath = "/api/v1/auth"

// Session is a freshly minted pair: a short-lived access token for the client to
// hold in memory, and a refresh token for the cookie.
type Session struct {
	AccessToken      string
	RefreshToken     string
	RefreshExpiresAt time.Time
	User             User
}

// IssueSession mints an access token and a stored refresh token for a user.
// Exported so sibling modules that legitimately create a session (org invitation
// acceptance) don't reimplement any of it.
func IssueSession(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, u User) (Session, error) {
	access, err := issueAccessToken(cfg, u)
	if err != nil {
		return Session{}, err
	}

	raw, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	expiresAt := time.Now().Add(cfg.JWTRefreshTTL)

	s := &store{pool: pool}
	if err := s.createRefreshToken(ctx, u.ID, hashToken(raw), expiresAt); err != nil {
		return Session{}, err
	}

	return Session{
		AccessToken: access, RefreshToken: raw, RefreshExpiresAt: expiresAt, User: u,
	}, nil
}

// Refresh rotates a refresh token: the presented one is revoked and a successor
// issued, together with a new access token.
//
// Rotation with reuse detection. Because each token is single-use, presenting one
// that is *already revoked* means the value was replayed — either a stolen copy
// or a client bug. The safe response is to assume theft and revoke every session
// for that user, forcing a real login.
func (s *Service) Refresh(ctx context.Context, raw string) (Session, error) {
	if strings.TrimSpace(raw) == "" {
		return Session{}, ErrInvalidRefresh
	}

	row, err := s.store.refreshTokenByHash(ctx, hashToken(raw))
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrInvalidRefresh
	}
	if err != nil {
		return Session{}, err
	}

	if row.RevokedAt != nil {
		// Replay of a spent token — treat the whole family as compromised.
		if err := s.store.revokeAllRefreshTokens(ctx, row.UserID); err != nil {
			return Session{}, err
		}
		return Session{}, ErrInvalidRefresh
	}
	if row.ExpiresAt.Before(time.Now()) {
		return Session{}, ErrInvalidRefresh
	}

	u, err := s.store.userByID(ctx, row.UserID)
	if err != nil {
		return Session{}, err
	}

	access, err := issueAccessToken(s.cfg, u)
	if err != nil {
		return Session{}, err
	}
	next, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	expiresAt := time.Now().Add(s.cfg.JWTRefreshTTL)

	if err := s.store.rotateRefreshToken(ctx, row.ID, u.ID, hashToken(next), expiresAt); err != nil {
		return Session{}, err
	}

	return Session{
		AccessToken: access, RefreshToken: next, RefreshExpiresAt: expiresAt, User: u,
	}, nil
}

// Logout revokes a single refresh token. A token that is already gone is not an
// error: signing out twice should still leave the caller signed out.
func (s *Service) Logout(ctx context.Context, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return s.store.revokeRefreshToken(ctx, hashToken(raw))
}

// SetRefreshCookie writes the refresh cookie. Exported for the org module's
// invitation acceptance, which also starts a session.
//
// HttpOnly so script can't read it, Path-scoped to the auth routes, and
// SameSite=Lax: Lax already blocks the cross-site POST a CSRF attempt would need,
// while still working for an app and API on different ports or subdomains of one
// site. Secure follows the deployment scheme.
func SetRefreshCookie(w http.ResponseWriter, cfg config.Config, raw string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    raw,
		Path:     refreshCookiePath,
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
		HttpOnly: true,
		Secure:   secureCookies(cfg),
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearRefreshCookie removes the cookie on logout.
func ClearRefreshCookie(w http.ResponseWriter, cfg config.Config) {
	http.SetCookie(w, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    "",
		Path:     refreshCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secureCookies(cfg),
		SameSite: http.SameSiteLaxMode,
	})
}

// secureCookies mirrors the SSO state cookie's rule: infer from the public URLs
// rather than a separate flag that can disagree with reality.
func secureCookies(cfg config.Config) bool {
	return strings.HasPrefix(cfg.WebAppURL, "https://") ||
		strings.HasPrefix(cfg.OIDCRedirectBase, "https://")
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
