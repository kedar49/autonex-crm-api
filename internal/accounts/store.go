// Package accounts is the CRM accounts (companies) domain module. An "account"
// here is a company the tenant sells to — not the tenant itself, which is an
// organization (see EXPLAINER §13).
//
// In this deployment the companies a lead or deal belongs to live in the
// `companies` table: contacts.company_id, deals.company_id and leads.company_id
// all point there, and it holds the real data. The vestigial `accounts` table is
// not used. This module therefore reads and writes `companies`, which is also
// what makes the per-account contact and deal counts meaningful.
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
	// ErrNotFound means no account with that id exists.
	ErrNotFound = errors.New("account not found")
	// ErrOwnerNotFound means the assignee does not exist.
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
//
// `companies` carries a domain rather than a full website URL, and has no phone
// or notes column; those are selected as typed NULLs so the scan positions and
// the JSON contract are unchanged.
const accountColumns = `
	a.id::text, a.name,
	a.domain          AS website,
	a.industry,
	NULL::text        AS phone,
	NULL::text        AS notes,
	a.owner_id::text  AS owner_user_id,
	p.full_name       AS owner_name,
	NULL::text        AS owner_email,
	a.created_at, a.updated_at,
	(SELECT count(*) FROM contacts c WHERE c.company_id = a.id AND c.deleted_at IS NULL),
	(SELECT count(*) FROM deals    d WHERE d.company_id = a.id AND d.deleted_at IS NULL)`

const accountFrom = ` FROM companies a LEFT JOIN profiles p ON p.id = a.owner_id `

func (s *store) list(ctx context.Context, orgID string, limit, offset int) ([]Account, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+accountColumns+accountFrom+
			`WHERE a.deleted_at IS NULL
			 ORDER BY a.created_at DESC, a.id
			 LIMIT $1 OFFSET $2`, limit, offset)
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
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM companies WHERE deleted_at IS NULL`).Scan(&n)
	return n, err
}

func (s *store) get(ctx context.Context, orgID, id string) (Account, error) {
	return scanAccount(s.pool.QueryRow(ctx,
		`SELECT `+accountColumns+accountFrom+`WHERE a.id = $1 AND a.deleted_at IS NULL`, id))
}

// create records a company. Phone and notes have no column here, so they are
// accepted by the API and dropped rather than failing the insert.
func (s *store) create(ctx context.Context, orgID string, in Input) (Account, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`INSERT INTO companies (name, domain, industry, owner_id)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id::text`,
		in.Name, in.Website, in.Industry, in.OwnerUserID).Scan(&id)
	if err != nil {
		return Account{}, translate(err)
	}
	return s.get(ctx, orgID, id)
}

func (s *store) update(ctx context.Context, orgID, id string, in Input) (Account, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE companies
		 SET name = $2, domain = $3, industry = $4, owner_id = $5, updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`,
		id, in.Name, in.Website, in.Industry, in.OwnerUserID)
	if err != nil {
		return Account{}, translate(err)
	}
	if tag.RowsAffected() == 0 {
		return Account{}, ErrNotFound
	}
	return s.get(ctx, orgID, id)
}

// delete removes a company only when nothing points at it. Deleting a linked
// company would orphan contacts and cascade into deals, so refusing is the safe
// reading of an ambiguous request; the caller can unlink first.
func (s *store) delete(ctx context.Context, orgID, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var contacts, deals int
	if err := tx.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM contacts WHERE company_id = $1),
		        (SELECT count(*) FROM deals    WHERE company_id = $1)`,
		id).Scan(&contacts, &deals); err != nil {
		return translate(err)
	}
	if contacts > 0 || deals > 0 {
		return ErrInUse
	}

	tag, err := tx.Exec(ctx, `DELETE FROM companies WHERE id = $1`, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// ownerInOrg reports whether the assignee exists. Owners are profiles in this
// schema, and the deployment is single-tenant, so existence is the whole check.
func (s *store) ownerInOrg(ctx context.Context, orgID, userID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM profiles WHERE id = $1)`, userID).Scan(&exists)
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
