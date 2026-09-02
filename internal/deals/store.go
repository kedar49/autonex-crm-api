package deals

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound means no deal with that id exists.
	ErrNotFound = errors.New("deal not found")
	// ErrRefNotFound means a referenced owner or contact doesn't exist.
	ErrRefNotFound = errors.New("referenced record is not in this organization")
)

const (
	pgInvalidTextRepr     = "22P02" // e.g. "abc" where a UUID is expected
	pgCheckViolation      = "23514" // the stage CHECK constraint
	pgForeignKeyViolation = "23503"
)

// positionGap is the spacing between cards in a column.
const positionGap = 1000

// Deal is the module's view of a row, including the denormalized owner and
// contact labels the board renders on each card.
type Deal struct {
	ID                string     `json:"id"`
	Title             string     `json:"title"`
	Description       *string    `json:"description"`
	Amount            float64    `json:"amount"`
	Stage             string     `json:"stage"`
	OwnerUserID       *string    `json:"ownerUserId"`
	OwnerName         *string    `json:"ownerName"`
	OwnerEmail        *string    `json:"ownerEmail"`
	ContactID         *string    `json:"contactId"`
	ContactName       *string    `json:"contactName"`
	AccountID         *string    `json:"accountId"`
	ExpectedCloseDate *time.Time `json:"expectedCloseDate"`
	Position          float64    `json:"position"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

type store struct {
	pool *pgxpool.Pool
}

// This deployment's deals table names things differently from the original
// model: notes rather than description, owner_id into profiles rather than
// owner_user_id into users, primary_contact_id rather than contact_id, and a
// company link rather than an account one. dealColumns maps those onto the
// scan positions scanDeal already reads, so the Go model, the JSON contract and
// the board component stay unchanged.
//
// There is no position column here, so a card's position is derived from its
// creation order within the stage. Board ordering is therefore stable and
// meaningful, but a manual reorder inside a column cannot be persisted — see
// move.
const dealColumns = `
	d.id::text, d.title,
	d.notes                    AS description,
	d.amount::float8, d.stage,
	d.owner_id::text           AS owner_user_id,
	p.full_name                AS owner_name,
	NULL::text                 AS owner_email,
	d.primary_contact_id::text AS contact_id,
	NULLIF(concat_ws(' ', c.first_name, c.last_name), ''),
	d.company_id::text         AS account_id,
	d.expected_close_date,
	(row_number() OVER (PARTITION BY d.stage ORDER BY d.created_at, d.id) * 1000)::float8,
	d.created_at, d.updated_at`

const dealFrom = `
	FROM deals d
	LEFT JOIN profiles p ON p.id = d.owner_id
	LEFT JOIN contacts c ON c.id = d.primary_contact_id `

func (s *store) board(ctx context.Context, orgID string, limit int) ([]Deal, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+dealColumns+dealFrom+
			`WHERE d.deleted_at IS NULL
			 ORDER BY d.stage, d.created_at, d.id
			 LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Deal, 0, 64)
	for rows.Next() {
		d, err := scanDeal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *store) get(ctx context.Context, orgID, id string) (Deal, error) {
	return scanDeal(s.pool.QueryRow(ctx,
		`SELECT `+dealColumns+dealFrom+`WHERE d.id = $1 AND d.deleted_at IS NULL`, id))
}

// create adds the deal to its stage. The account link is company_id: an account
// is a company in this schema, so the accountId the client sends is stored
// there.
func (s *store) create(ctx context.Context, orgID string, in Input) (Deal, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`INSERT INTO deals
		   (title, notes, amount, stage, owner_id, primary_contact_id,
		    company_id, expected_close_date)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id::text`,
		in.Title, in.Description, in.Amount, in.Stage, in.OwnerUserID,
		in.ContactID, in.AccountID, in.ExpectedCloseDate,
	).Scan(&id)
	if err != nil {
		return Deal{}, translate(err)
	}
	return s.get(ctx, orgID, id)
}

func (s *store) update(ctx context.Context, orgID, id string, in Input) (Deal, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE deals
		 SET title = $2, notes = $3, amount = $4, stage = $5,
		     owner_id = $6, primary_contact_id = $7, company_id = $8,
		     expected_close_date = $9, updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`,
		id, in.Title, in.Description, in.Amount, in.Stage,
		in.OwnerUserID, in.ContactID, in.AccountID, in.ExpectedCloseDate)
	if err != nil {
		return Deal{}, translate(err)
	}
	if tag.RowsAffected() == 0 {
		return Deal{}, ErrNotFound
	}
	return s.get(ctx, orgID, id)
}

func (s *store) delete(ctx context.Context, orgID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM deals WHERE id = $1`, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// move changes which column a card sits in.
//
// The original implementation also renumbered a position column to honour the
// drop index. This schema has no such column, so the index is accepted and
// ignored: dragging a card between columns persists, dragging it within one
// does not survive a reload. Adding a position column is the only way to change
// that, which would be a schema change rather than a code one.
func (s *store) move(ctx context.Context, orgID, id, stage string, index int) (Deal, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE deals SET stage = $2, updated_at = now()
		  WHERE id = $1 AND deleted_at IS NULL`, id, stage)
	if err != nil {
		return Deal{}, translate(err)
	}
	if tag.RowsAffected() == 0 {
		return Deal{}, ErrNotFound
	}
	return s.get(ctx, orgID, id)
}

// refInOrg checks a client-supplied foreign key. Single-tenant here, so
// existence is the only thing left to verify.
func (s *store) refInOrg(ctx context.Context, table, orgID, id string) (bool, error) {
	if table == "accounts" {
		table = "companies"
	}
	if table == "users" || table == "profiles" {
		var exists bool
		err := s.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM users WHERE id = $1) OR EXISTS (SELECT 1 FROM profiles WHERE id = $1)`, id).Scan(&exists)
		if err != nil {
			if isPgCode(err, pgInvalidTextRepr) {
				return false, nil
			}
			return false, err
		}
		return exists, nil
	}
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM `+table+` WHERE id = $1)`, id).Scan(&exists)
	if err != nil {
		if isPgCode(err, pgInvalidTextRepr) {
			return false, nil
		}
		return false, err
	}
	return exists, nil
}

// Stats backs the dashboard: per-stage counts and amounts in one pass.
type Stats struct {
	Stage  string  `json:"stage"`
	Count  int     `json:"count"`
	Amount float64 `json:"amount"`
}

func (s *store) stats(ctx context.Context, orgID string) ([]Stats, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT stage, count(*), COALESCE(sum(amount), 0)::float8
		 FROM deals WHERE deleted_at IS NULL GROUP BY stage`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Stats, 0, len(Stages))
	for rows.Next() {
		var st Stats
		if err := rows.Scan(&st.Stage, &st.Count, &st.Amount); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanDeal(row rowScanner) (Deal, error) {
	var d Deal
	err := row.Scan(
		&d.ID, &d.Title, &d.Description, &d.Amount, &d.Stage,
		&d.OwnerUserID, &d.OwnerName, &d.OwnerEmail,
		&d.ContactID, &d.ContactName,
		&d.AccountID, &d.ExpectedCloseDate, &d.Position, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return Deal{}, translate(err)
	}
	return d, nil
}

// translate maps pgx/Postgres failures onto the module's domain errors.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows), isPgCode(err, pgInvalidTextRepr):
		return ErrNotFound
	case isPgCode(err, pgForeignKeyViolation):
		return ErrRefNotFound
	case isPgCode(err, pgCheckViolation):
		// Only the stage CHECK can fire here; the service validates stages
		// first, so this is the belt to that braces.
		return ErrNotFound
	default:
		return err
	}
}

func isPgCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
