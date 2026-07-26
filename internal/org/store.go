package org

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound covers a missing (or other-org) invitation.
	ErrNotFound = errors.New("not found")
	// ErrAlreadyMember means the email already belongs to a user somewhere.
	ErrAlreadyMember = errors.New("already a member")
	// ErrAlreadyInvited means a pending invitation for that email exists.
	ErrAlreadyInvited = errors.New("already invited")
	// ErrInviteInvalid covers an unknown, expired, or already-accepted token.
	ErrInviteInvalid = errors.New("invitation is invalid or has expired")
)

const pgUniqueViolation = "23505"

// Member is one user of an organization, as shown in the team list and the
// lead-owner picker.
type Member struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Name         *string   `json:"name"`
	AuthProvider string    `json:"authProvider"`
	CreatedAt    time.Time `json:"createdAt"`
}

// Invitation is a pending (or historical) invite. The token itself is never
// stored or returned — only its hash lives in the database.
type Invitation struct {
	ID         string     `json:"id"`
	Email      string     `json:"email"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	CreatedAt  time.Time  `json:"createdAt"`
	AcceptedAt *time.Time `json:"acceptedAt"`
}

type store struct {
	pool *pgxpool.Pool
}

func (s *store) members(ctx context.Context, orgID string) ([]Member, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id::text, email, name, auth_provider, created_at
		 FROM users WHERE org_id = $1 ORDER BY created_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Member, 0, 8)
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.Email, &m.Name, &m.AuthProvider, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// userExists reports whether any user anywhere holds this email. Users are
// globally unique by email, so an invite can't be accepted into a second org.
func (s *store) userExists(ctx context.Context, email string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE email = $1)`, email).Scan(&exists)
	return exists, err
}

func (s *store) createInvitation(
	ctx context.Context, orgID, email, tokenHash, invitedBy string, expiresAt time.Time,
) (Invitation, error) {
	var inv Invitation
	err := s.pool.QueryRow(ctx,
		`INSERT INTO invitations (org_id, email, token_hash, invited_by, expires_at)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id::text, email, expires_at, created_at, accepted_at`,
		orgID, email, tokenHash, invitedBy, expiresAt,
	).Scan(&inv.ID, &inv.Email, &inv.ExpiresAt, &inv.CreatedAt, &inv.AcceptedAt)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		return Invitation{}, ErrAlreadyInvited
	}
	return inv, err
}

func (s *store) pendingInvitations(ctx context.Context, orgID string) ([]Invitation, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id::text, email, expires_at, created_at, accepted_at
		 FROM invitations
		 WHERE org_id = $1 AND accepted_at IS NULL
		 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Invitation, 0, 4)
	for rows.Next() {
		var inv Invitation
		if err := rows.Scan(&inv.ID, &inv.Email, &inv.ExpiresAt, &inv.CreatedAt, &inv.AcceptedAt); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (s *store) revokeInvitation(ctx context.Context, orgID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM invitations WHERE org_id = $1 AND id = $2 AND accepted_at IS NULL`, orgID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// acceptedUser is the newly created teammate, enough to mint their token.
type acceptedUser struct {
	ID    string
	Email string
	OrgID string
}

// acceptInvitation consumes a valid invitation and creates its user, atomically.
//
// The invitation row is claimed with a conditional UPDATE (accepted_at IS NULL),
// so two clients racing on the same link produce exactly one member: the loser
// updates zero rows and gets ErrInviteInvalid.
func (s *store) acceptInvitation(ctx context.Context, tokenHash, name, passwordHash string) (acceptedUser, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return acceptedUser{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orgID, email string
	err = tx.QueryRow(ctx,
		`UPDATE invitations
		 SET accepted_at = now()
		 WHERE token_hash = $1 AND accepted_at IS NULL AND expires_at > now()
		 RETURNING org_id::text, email`, tokenHash).Scan(&orgID, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return acceptedUser{}, ErrInviteInvalid
	}
	if err != nil {
		return acceptedUser{}, err
	}

	var userID string
	err = tx.QueryRow(ctx,
		`INSERT INTO users (email, name, org_id, password_hash, auth_provider)
		 VALUES ($1, $2, $3, $4, 'password')
		 RETURNING id::text`,
		email, nilIfEmpty(name), orgID, passwordHash).Scan(&userID)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		// Someone registered with this email between invite and acceptance.
		return acceptedUser{}, ErrAlreadyMember
	}
	if err != nil {
		return acceptedUser{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return acceptedUser{}, err
	}
	return acceptedUser{ID: userID, Email: email, OrgID: orgID}, nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
