package org

import (
	"context"
	"errors"
	"time"

	"github.com/Autonex009/autonex-crm-api/pkg/database"
	"github.com/jackc/pgx/v5"
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

// Member is one user of an organization, as shown in the team list and the
// lead-owner picker.
type Member struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Name         *string   `json:"name"`
	AuthProvider string    `json:"authProvider"`
	Role         string    `json:"role"`
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

// Workspace is the organization itself — the settings every surface needs.
type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Currency is the ISO 4217 code every amount in this workspace is in. One
	// per organization, not per record — see migration 000006.
	Currency string `json:"currency"`
}

type store struct {
	pool *pgxpool.Pool
}

func (s *store) workspace(ctx context.Context, orgID string) (Workspace, error) {
	var w Workspace
	err := s.pool.QueryRow(ctx,
		`SELECT id::text, name, currency FROM organizations WHERE id = $1`, orgID,
	).Scan(&w.ID, &w.Name, &w.Currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	return w, err
}

// updateWorkspace applies a partial update: a nil field is left alone.
func (s *store) updateWorkspace(ctx context.Context, orgID string, name, currency *string) (Workspace, error) {
	var w Workspace
	err := s.pool.QueryRow(ctx,
		`UPDATE organizations
		 SET name = COALESCE($2, name), currency = COALESCE($3, currency)
		 WHERE id = $1
		 RETURNING id::text, name, currency`,
		orgID, name, currency,
	).Scan(&w.ID, &w.Name, &w.Currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	return w, err
}

func (s *store) members(ctx context.Context, orgID string) ([]Member, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT u.id::text, u.email, u.name, u.auth_provider, coalesce(p.role, 'sales'), u.created_at
		 FROM users u
		 LEFT JOIN profiles p ON p.id = u.id
		 WHERE u.org_id = $1 ORDER BY u.created_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Member, 0, 8)
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.Email, &m.Name, &m.AuthProvider, &m.Role, &m.CreatedAt); err != nil {
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

	if database.IsUniqueViolation(err) {
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
	Role  string
}

// Role-change failures the handler turns into 4xx answers rather than 500s.
var (
	// ErrMemberNotFound means the member doesn't exist in the organization.
	ErrMemberNotFound = errors.New("member not found")
	// ErrSelfRoleChange guards against an admin promoting themselves.
	ErrSelfRoleChange = errors.New("you cannot change your own role")
	// ErrOwnerOnly means the change needs owner rights the caller lacks.
	ErrOwnerOnly = errors.New("only an owner can do that")
	// ErrLastOwner means the change would leave the org with no owner.
	ErrLastOwner = errors.New("the organisation must keep at least one owner")
)

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

	if database.IsUniqueViolation(err) {
		// Someone registered with this email between invite and acceptance.
		return acceptedUser{}, ErrAlreadyMember
	}
	if err != nil {
		return acceptedUser{}, err
	}

	fullName := name
	if fullName == "" {
		fullName = email
	}
	const role = "sales"
	if _, err := tx.Exec(ctx,
		`INSERT INTO profiles (id, full_name, role)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (id) DO UPDATE SET full_name = EXCLUDED.full_name`,
		userID, fullName, role,
	); err != nil {
		return acceptedUser{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return acceptedUser{}, err
	}
	return acceptedUser{ID: userID, Email: email, OrgID: orgID, Role: role}, nil
}

// updateMemberRole writes a member's profile role. Reading the current role,
// counting the org's owners and writing the new role all share one transaction
// so two concurrent demotions can't race past the last-owner check.
func (s *store) updateMemberRole(ctx context.Context, orgID, actorRole, memberID, role string) (Member, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Member{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current string
	err = tx.QueryRow(ctx,
		`SELECT coalesce(p.role, 'sales')
		 FROM users u
		 LEFT JOIN profiles p ON p.id = u.id
		 WHERE u.org_id = $1::uuid AND u.id = $2::uuid
		 FOR UPDATE OF u`,
		orgID, memberID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) || database.IsInvalidTextRepr(err) {
		return Member{}, ErrMemberNotFound
	}
	if err != nil {
		return Member{}, err
	}

	// Unseating an owner is an owner's prerogative, not an admin's.
	if current == "owner" && actorRole != "owner" {
		return Member{}, ErrOwnerOnly
	}
	if current == "owner" && role != "owner" {
		var owners int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM users u
			 JOIN profiles p ON p.id = u.id
			 WHERE u.org_id = $1::uuid AND p.role = 'owner'`, orgID).Scan(&owners); err != nil {
			return Member{}, err
		}
		if owners <= 1 {
			return Member{}, ErrLastOwner
		}
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO profiles (id, full_name, role)
		 SELECT u.id, coalesce(u.name, split_part(u.email, '@', 1)), $3
		 FROM users u WHERE u.id = $2::uuid AND u.org_id = $1::uuid
		 ON CONFLICT (id) DO UPDATE SET role = EXCLUDED.role, updated_at = now()`,
		orgID, memberID, role,
	); err != nil {
		return Member{}, err
	}

	var m Member
	if err := tx.QueryRow(ctx,
		`SELECT u.id::text, u.email, u.name, u.auth_provider, coalesce(p.role, 'sales'), u.created_at
		 FROM users u
		 LEFT JOIN profiles p ON p.id = u.id
		 WHERE u.org_id = $1::uuid AND u.id = $2::uuid`, orgID, memberID,
	).Scan(&m.ID, &m.Email, &m.Name, &m.AuthProvider, &m.Role, &m.CreatedAt); err != nil {
		return Member{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Member{}, err
	}
	return m, nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
