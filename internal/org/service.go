package org

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/go-crm/services/internal/auth"
	"github.com/go-crm/services/pkg/config"
)

// inviteTTL is how long an invitation link stays usable.
const inviteTTL = 7 * 24 * time.Hour

// ValidationError is a rejected input, reported to the client as a 400.
type ValidationError struct{ msg string }

func (e ValidationError) Error() string { return e.msg }

// IsValidation reports whether err is a client input error (→ 400).
func IsValidation(err error) bool {
	var ve ValidationError
	return errors.As(err, &ve)
}

// NewInvitation is what the inviter gets back: the stored row plus the one and
// only time the raw link is ever available.
type NewInvitation struct {
	Invitation
	// InviteURL is shown once, for the inviter to send on. There is no mail
	// sender wired up, so delivery is manual — see EXPLAINER §15.3.
	InviteURL string `json:"inviteUrl"`
}

// Session is returned when an invitation is accepted: the teammate is signed in
// straight away rather than bounced to the login form.
//
// Carries the refresh token so the handler can set the cookie — it is never
// serialized (see the `json:"-"` tags), because the whole point of the cookie is
// that script never sees the value.
type Session struct {
	Token            string    `json:"token"`
	User             auth.User `json:"user"`
	RefreshToken     string    `json:"-"`
	RefreshExpiresAt time.Time `json:"-"`
}

// Service holds the organization/teammate business logic.
type Service struct {
	store *store
	// pool is passed through to auth.IssueSession, which writes the refresh token.
	pool *pgxpool.Pool
	cfg  config.Config
}

func newService(pool *pgxpool.Pool, cfg config.Config) *Service {
	return &Service{store: &store{pool: pool}, pool: pool, cfg: cfg}
}

// Members lists the users of an organization.
func (s *Service) Members(ctx context.Context, orgID string) ([]Member, error) {
	return s.store.members(ctx, orgID)
}

// Workspace returns the organization's own settings.
func (s *Service) Workspace(ctx context.Context, orgID string) (Workspace, error) {
	return s.store.workspace(ctx, orgID)
}

// UpdateWorkspace applies a partial update to the organization's settings.
func (s *Service) UpdateWorkspace(ctx context.Context, orgID string, name, currency *string) (Workspace, error) {
	if name != nil {
		trimmed := strings.TrimSpace(*name)
		if trimmed == "" {
			return Workspace{}, ValidationError{msg: "workspace name cannot be empty"}
		}
		if len(trimmed) > 120 {
			return Workspace{}, ValidationError{msg: "workspace name must be 120 characters or fewer"}
		}
		name = &trimmed
	}
	if currency != nil {
		// The DB has a CHECK for this too; validating here turns a 500 into a
		// useful 400.
		code := strings.ToUpper(strings.TrimSpace(*currency))
		if !isCurrencyCode(code) {
			return Workspace{}, ValidationError{msg: "currency must be a 3-letter code, e.g. USD"}
		}
		currency = &code
	}
	return s.store.updateWorkspace(ctx, orgID, name, currency)
}

// isCurrencyCode checks the shape only — ISO 4217 has ~180 codes and hard-coding
// them would just be a list to maintain. Formatting falls back gracefully on an
// unknown-but-well-formed code.
func isCurrencyCode(code string) bool {
	if len(code) != 3 {
		return false
	}
	for _, r := range code {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// PendingInvitations lists invitations that have not been accepted yet.
func (s *Service) PendingInvitations(ctx context.Context, orgID string) ([]Invitation, error) {
	return s.store.pendingInvitations(ctx, orgID)
}

// Invite creates an invitation and returns the link to hand to the invitee.
func (s *Service) Invite(ctx context.Context, orgID, invitedBy, email string) (NewInvitation, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") || strings.ContainsAny(email, " \t") {
		return NewInvitation{}, ValidationError{msg: "a valid email is required"}
	}

	// Users are globally unique by email, so this would fail at acceptance time
	// anyway; catching it here gives a useful message instead.
	exists, err := s.store.userExists(ctx, email)
	if err != nil {
		return NewInvitation{}, err
	}
	if exists {
		return NewInvitation{}, ErrAlreadyMember
	}

	token, err := randomToken()
	if err != nil {
		return NewInvitation{}, err
	}
	inv, err := s.store.createInvitation(
		ctx, orgID, email, hashToken(token), invitedBy, time.Now().Add(inviteTTL))
	if err != nil {
		return NewInvitation{}, err
	}

	return NewInvitation{Invitation: inv, InviteURL: s.inviteURL(token)}, nil
}

// Revoke deletes a pending invitation.
func (s *Service) Revoke(ctx context.Context, orgID, id string) error {
	return s.store.revokeInvitation(ctx, orgID, id)
}

// Accept consumes an invitation token, creates the teammate, and signs them in.
func (s *Service) Accept(ctx context.Context, token, name, password string) (Session, error) {
	if strings.TrimSpace(token) == "" {
		return Session{}, ErrInviteInvalid
	}
	if len(password) < 8 {
		return Session{}, ValidationError{msg: "password must be at least 8 characters"}
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return Session{}, err
	}

	u, err := s.store.acceptInvitation(ctx, hashToken(token), strings.TrimSpace(name), hash)
	if err != nil {
		return Session{}, err
	}

	user := auth.User{
		ID: u.ID, Email: u.Email, OrgID: u.OrgID,
		Name: nilIfEmpty(strings.TrimSpace(name)), AuthProvider: "password",
	}

	// Auth owns session minting, including the refresh token — this module just
	// asks for one rather than keeping a second implementation.
	session, err := auth.IssueSession(ctx, s.pool, s.cfg, user)
	if err != nil {
		return Session{}, err
	}
	return Session{
		Token:            session.AccessToken,
		User:             user,
		RefreshToken:     session.RefreshToken,
		RefreshExpiresAt: session.RefreshExpiresAt,
	}, nil
}

// inviteURL points at the SPA page that collects a name and password. The token
// rides in the fragment for the same reason the SSO token does: fragments are
// never sent to a server, so the link can't leak through access logs.
//
// The key is "invite", not "token": the SPA captures "#token=" on boot as an SSO
// access token, and an invite token arriving under that name would be swallowed
// as a (bogus) session before the accept page ever saw it.
func (s *Service) inviteURL(token string) string {
	return fmt.Sprintf("%s/app/accept-invite#invite=%s",
		strings.TrimRight(s.cfg.WebAppURL, "/"), token)
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken is a plain SHA-256: the token is 32 bytes of CSPRNG output, so it
// has no entropy problem that a slow KDF would need to compensate for.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
