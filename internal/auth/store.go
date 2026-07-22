package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrUserNotFound is returned by the store when no row matches the lookup.
var ErrUserNotFound = errors.New("user not found")

// User is the auth module's view of a row in the users table.
type User struct {
	ID             string  `json:"id"`
	Email          string  `json:"email"`
	PasswordHash   *string `json:"-"`
	AuthProvider   string  `json:"authProvider"`
	ProviderUserID *string `json:"-"`
}

// store is a thin, hand-written pgx repository for the users table. The repo's
// documented pattern uses sqlc (see internal/auth/db/queries.sql); this keeps
// the module buildable without the sqlc codegen step. Swap in the generated
// package later without touching service.go.
type store struct {
	pool *pgxpool.Pool
}

const userColumns = `id::text, email, password_hash, auth_provider, provider_user_id`

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

func (s *store) createUser(ctx context.Context, email, passwordHash string) (User, error) {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, auth_provider)
		 VALUES ($1, $2, 'password')
		 RETURNING `+userColumns, email, passwordHash)
	return scanUser(row)
}

func (s *store) createSSOUser(ctx context.Context, email, provider, providerUserID string) (User, error) {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO users (email, auth_provider, provider_user_id)
		 VALUES ($1, $2, $3)
		 RETURNING `+userColumns, email, provider, providerUserID)
	return scanUser(row)
}

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.AuthProvider, &u.ProviderUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, err
	}
	return u, nil
}
