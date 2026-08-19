// Package accounts is the CRM accounts (companies) domain module. An "account"
// here is a company the tenant sells to — not the tenant itself, which is an
// organization (see EXPLAINER §13).
package accounts

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound means no account with that id exists in the caller's org. A row
	// belonging to another tenant is indistinguishable from a missing one.
	ErrNotFound = errors.New("account not found")
	// ErrOwnerNotFound means the assignee is not a member of the caller's org.
	ErrOwnerNotFound = errors.New("owner is not a member of this organization")
	// ErrInUse means contacts or deals still reference the account.
	ErrInUse = errors.New("account is still referenced")
)

const (
	pgInvalidTextRepr     = "22P02"
	pgForeignKeyViolation = "23503"
)

// Account is the module's view of a row, plus the two counts a list needs to be
// useful and the denormalized owner label.
type Account struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Website     *string   `json:"website"`
	Industry    *string   `json:"industry"`
	Phone       *string   `json:"phone"`
	Notes       *string   `json:"notes"`
	OwnerUserID *string   `json:"ownerUserId"`
	OwnerName   *string   `json:"ownerName"`
	OwnerEmail  *string   `json:"ownerEmail"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	// Counts of what hangs off this company. Answering "can I delete this?" and
	// "is this account real?" without a second round-trip per row.
	ContactCount int `json:"contactCount"`
	DealCount    int `json:"dealCount"`
}

type store struct {
	pool *pgxpool.Pool
}

// The counts are correlated subqueries rather than GROUP BY joins: with two
// different child tables a join would multiply rows and need DISTINCT, and at
// list-page sizes (25) the planner turns these into cheap index lookups.
const accountColumns = `
	a.id::text, a.name, a.website, a.industry, a.phone, a.notes,
	a.owner_user_id::text, u.name, u.email, a.created_at, a.updated_at,
	(SELECT count(*) FROM contacts c WHERE c.account_id = a.id),
	(SELECT count(*) FROM deals d WHERE d.account_id = a.id)`

const accountFrom = ` FROM accounts a LEFT JOIN users u ON u.id = a.owner_user_id `

func (s *store) list(ctx context.Context, orgID string, limit, offset int) ([]Account, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+accountColumns+accountFrom+
			`WHERE a.org_id = $1
			 ORDER BY a.created_at DESC, a.id
			 LIMIT $2 OFFSET $3`, orgID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Non-nil so an empty page marshals as [] rather than null.
	out := make([]Account, 0, limit)
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *store) count(ctx context.Context, orgID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE org_id = $1`, orgID).Scan(&n)
	return n, err
}

func (s *store) get(ctx context.Context, orgID, id string) (Account, error) {
	return scanAccount(s.pool.QueryRow(ctx,
		`SELECT `+accountColumns+accountFrom+`WHERE a.org_id = $1 AND a.id = $2`, orgID, id))
}

func (s *store) create(ctx context.Context, orgID string, in Input) (Account, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`INSERT INTO accounts (org_id, name, website, industry, phone, notes, owner_user_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id::text`,
		orgID, in.Name, in.Website, in.Industry, in.Phone, in.Notes, in.OwnerUserID).Scan(&id)
	if err != nil {
		return Account{}, translate(err)
	}
	return s.get(ctx, orgID, id)
}

func (s *store) update(ctx context.Context, orgID, id string, in Input) (Account, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE accounts
		 SET name = $3, website = $4, industry = $5, phone = $6, notes = $7,
		     owner_user_id = $8, updated_at = now()
		 WHERE org_id = $1 AND id = $2`,
		orgID, id, in.Name, in.Website, in.Industry, in.Phone, in.Notes, in.OwnerUserID)
	if err != nil {
		return Account{}, translate(err)
	}
	if tag.RowsAffected() == 0 {
		return Account{}, ErrNotFound
	}
	return s.get(ctx, orgID, id)
}

// delete removes an account only when nothing points at it.
//
// contacts.account_id is ON DELETE SET NULL and deals.account_id ON DELETE
// CASCADE (from 000001) — so deleting a linked account would silently orphan
// contacts and *destroy deals*. Refusing is the safe reading of an ambiguous
// request; the caller can unlink first.
func (s *store) delete(ctx context.Context, orgID, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var contacts, deals int
	if err := tx.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM contacts WHERE org_id = $1 AND account_id = $2),
		        (SELECT count(*) FROM deals    WHERE org_id = $1 AND account_id = $2)`,
		orgID, id).Scan(&contacts, &deals); err != nil {
		return translate(err)
	}
	if contacts > 0 || deals > 0 {
		return ErrInUse
	}

	tag, err := tx.Exec(ctx, `DELETE FROM accounts WHERE org_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// ownerInOrg reports whether the user is a member of the caller's org. Without
// this a client could assign an account to a user in another tenant.
func (s *store) ownerInOrg(ctx context.Context, orgID, userID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE org_id = $1 AND id = $2)`,
		orgID, userID).Scan(&exists)
	if err != nil {
		if isPgCode(err, pgInvalidTextRepr) {
			return false, nil
		}
		return false, err
	}
	return exists, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAccount(row rowScanner) (Account, error) {
	var a Account
	err := row.Scan(
		&a.ID, &a.Name, &a.Website, &a.Industry, &a.Phone, &a.Notes,
		&a.OwnerUserID, &a.OwnerName, &a.OwnerEmail, &a.CreatedAt, &a.UpdatedAt,
		&a.ContactCount, &a.DealCount)
	if err != nil {
		return Account{}, translate(err)
	}
	return a, nil
}

func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows), isPgCode(err, pgInvalidTextRepr):
		return ErrNotFound
	case isPgCode(err, pgForeignKeyViolation):
		return ErrOwnerNotFound
	default:
		return err
	}
}

func isPgCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
