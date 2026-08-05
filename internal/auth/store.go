package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrUserNotFound is returned by the store when no row matches the lookup.
var ErrUserNotFound = errors.New("user not found")

// User is the auth module's view of a row in the users table.
type User struct {
	ID    string  `json:"id"`
	Email string  `json:"email"`
	Name  *string `json:"name"`
	// OrgID is the tenant every request from this user is scoped to.
	OrgID          string  `json:"orgId"`
	PasswordHash   *string `json:"-"`
	AuthProvider   string  `json:"authProvider"`
	ProviderUserID *string `json:"-"`
}

// newUser is the input to createUserWithOrg — one shape for both the password and
// the SSO path (each leaves the other's fields nil).
type newUser struct {
	Email          string
	OrgName        string
	PasswordHash   *string
	AuthProvider   string
	ProviderUserID *string
}

// store is a thin, hand-written pgx repository for the users table. The repo's
// documented pattern uses sqlc (see internal/auth/db/queries.sql); this keeps
// the module buildable without the sqlc codegen step. Swap in the generated
// package later without touching service.go.
type store struct {
	pool *pgxpool.Pool
}

const userColumns = `id::text, email, name, org_id::text, password_hash, auth_provider, provider_user_id`

func (s *store) userByEmail(ctx context.Context, email string) (User, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE email = $1`, email)
	return scanUser(row)
}

func (s *store) userByProvider(ctx context.Context, provider, providerUserID string) (User, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE auth_provider = $1 AND provider_user_id = $2`,
		provider, providerUserID)
	return scanUser(row)
}

// createUserWithOrg provisions a personal organization and its first user in a
// single transaction. users.org_id is NOT NULL and every CRM query is scoped by
// it, so a user without an org could not reach any data — the two rows have to
// be created atomically or not at all.
func (s *store) createUserWithOrg(ctx context.Context, in newUser) (User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	// No-op once the tx is committed; rolls back on any early return.
	defer func() { _ = tx.Rollback(ctx) }()

	var orgID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO organizations (name) VALUES ($1) RETURNING id::text`,
		in.OrgName,
	).Scan(&orgID); err != nil {
		return User{}, err
	}

	u, err := scanUser(tx.QueryRow(ctx,
		`INSERT INTO users (email, org_id, password_hash, auth_provider, provider_user_id)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING `+userColumns,
		in.Email, orgID, in.PasswordHash, in.AuthProvider, in.ProviderUserID))
	if err != nil {
		return User{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return User{}, err
	}
	return u, nil
}

func (s *store) userByID(ctx context.Context, id string) (User, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id)
	return scanUser(row)
}

// refreshTokenRow is the stored side of a refresh token; the raw value is never
// persisted (see refresh.go).
type refreshTokenRow struct {
	ID        string
	UserID    string
	ExpiresAt time.Time
	RevokedAt *time.Time
}

func (s *store) createRefreshToken(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		userID, tokenHash, expiresAt)
	return err
}

func (s *store) refreshTokenByHash(ctx context.Context, tokenHash string) (refreshTokenRow, error) {
	var r refreshTokenRow
	err := s.pool.QueryRow(ctx,
		`SELECT id::text, user_id::text, expires_at, revoked_at
		 FROM refresh_tokens WHERE token_hash = $1`, tokenHash,
	).Scan(&r.ID, &r.UserID, &r.ExpiresAt, &r.RevokedAt)
	return r, err
}

// rotateRefreshToken revokes the presented token and issues its successor in one
// transaction — a rotation that revoked without replacing would sign the user out.
func (s *store) rotateRefreshToken(
	ctx context.Context, oldID, userID, newHash string, expiresAt time.Time,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var newID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, $2, $3) RETURNING id::text`,
		userID, newHash, expiresAt).Scan(&newID); err != nil {
		return err
	}

	// The `revoked_at IS NULL` guard makes the rotation itself single-use: two
	// concurrent refreshes with the same token can't both succeed.
	tag, err := tx.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now(), replaced_by = $2
		 WHERE id = $1 AND revoked_at IS NULL`, oldID, newID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrInvalidRefresh
	}

	return tx.Commit(ctx)
}

func (s *store) revokeRefreshToken(ctx context.Context, tokenHash string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now()
		 WHERE token_hash = $1 AND revoked_at IS NULL`, tokenHash)
	return err
}

// revokeAllRefreshTokens is the reuse-detection response: assume the family is
// compromised and force a real login.
func (s *store) revokeAllRefreshTokens(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now()
		 WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	return err
}

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.OrgID, &u.PasswordHash, &u.AuthProvider, &u.ProviderUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, err
	}
	return u, nil
}
