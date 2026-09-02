package contacts

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound means no contact with that id exists *in the caller's org*.
	// A row belonging to another tenant is indistinguishable from a missing one,
	// which is deliberate: a 404 leaks nothing about other tenants' data.
	ErrNotFound = errors.New("contact not found")
	// ErrEmailTaken means the org already has a contact with that email.
	ErrEmailTaken = errors.New("contact email already exists")
	// ErrAccountNotFound means the referenced account is missing or belongs to
	// another org.
	ErrAccountNotFound = errors.New("account not found")
)

// Postgres error codes we translate into domain errors.
const (
	pgUniqueViolation     = "23505"
	pgInvalidTextRepr     = "22P02" // e.g. "abc" passed where a UUID is expected
	pgForeignKeyViolation = "23503"
)

// Contact is the contacts module's view of a row in the contacts table.
type Contact struct {
	ID        string    `json:"id"`
	FirstName string    `json:"firstName"`
	LastName  *string   `json:"lastName"`
	Email     *string   `json:"email"`
	Phone     *string   `json:"phone"`
	AccountID *string   `json:"accountId"`
	CreatedAt time.Time `json:"createdAt"`
}

// store is a hand-written pgx repository, matching the auth module's approach
// (see internal/contacts/db/queries.sql for the sqlc-shaped reference).
//
// Every method takes orgID and every statement filters on it. That is the only
// thing standing between two tenants' data, so it is not optional anywhere.
type store struct {
	pool *pgxpool.Pool
}

// A contact's company is company_id in this schema; it is aliased to the
// account_id position so the scan order and JSON contract are unchanged.
const contactColumns = `id::text, first_name, last_name, email, phone, company_id::text, created_at`

func (s *store) list(ctx context.Context, orgID string, limit, offset int) ([]Contact, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+contactColumns+`
		 FROM contacts
		 WHERE deleted_at IS NULL
		 ORDER BY created_at DESC, id
		 LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Non-nil so an empty page marshals as [] rather than null.
	out := make([]Contact, 0, limit)
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *store) count(ctx context.Context, orgID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM contacts WHERE deleted_at IS NULL`).Scan(&n)
	return n, err
}

func (s *store) get(ctx context.Context, orgID, id string) (Contact, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+contactColumns+` FROM contacts WHERE id = $1 AND deleted_at IS NULL`, id)
	return scanContact(row)
}

func (s *store) create(ctx context.Context, orgID string, in Input) (Contact, error) {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO contacts (first_name, last_name, email, phone, company_id)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING `+contactColumns,
		in.FirstName, in.LastName, in.Email, in.Phone, in.AccountID)
	return scanContact(row)
}

func (s *store) update(ctx context.Context, orgID, id string, in Input) (Contact, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE contacts
		 SET first_name = $2, last_name = $3, email = $4, phone = $5,
		     company_id = $6, updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL
		 RETURNING `+contactColumns,
		id, in.FirstName, in.LastName, in.Email, in.Phone, in.AccountID)
	return scanContact(row)
}

func (s *store) delete(ctx context.Context, orgID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM contacts WHERE id = $1`, id)
	if err != nil {
		if isPgCode(err, pgInvalidTextRepr) {
			return ErrNotFound
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// accountInOrg reports whether the company exists. Accounts are companies in
// this schema and the deployment is single-tenant, so existence is the whole
// check.
func (s *store) accountInOrg(ctx context.Context, orgID, accountID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM companies WHERE id = $1)`,
		accountID).Scan(&exists)
	if err != nil {
		if isPgCode(err, pgInvalidTextRepr) {
			return false, nil // malformed id can't name an account
		}
		return false, err
	}
	return exists, nil
}

// rowScanner is satisfied by both pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanContact(row rowScanner) (Contact, error) {
	var c Contact
	err := row.Scan(&c.ID, &c.FirstName, &c.LastName, &c.Email, &c.Phone, &c.AccountID, &c.CreatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Contact{}, ErrNotFound
	// A caller-supplied id that isn't a UUID is a miss, not a server error.
	case isPgCode(err, pgInvalidTextRepr):
		return Contact{}, ErrNotFound
	case isPgCode(err, pgUniqueViolation):
		return Contact{}, ErrEmailTaken
	case isPgCode(err, pgForeignKeyViolation):
		return Contact{}, ErrAccountNotFound
	case err != nil:
		return Contact{}, err
	}
	return c, nil
}

func isPgCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
