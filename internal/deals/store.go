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
	// ErrNotFound means no deal with that id exists in the caller's org. A deal
	// belonging to another tenant is indistinguishable from a missing one.
	ErrNotFound = errors.New("deal not found")
	// ErrRefNotFound means a referenced owner or contact isn't in the caller's org.
	ErrRefNotFound = errors.New("referenced record is not in this organization")
)

const (
	pgInvalidTextRepr     = "22P02" // e.g. "abc" where a UUID is expected
	pgCheckViolation      = "23514" // the stage CHECK constraint
	pgForeignKeyViolation = "23503"
)

// positionGap is the spacing between cards in a column. Positions are rewritten
// as multiples of this on every move, so gaps never need splitting.
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

// Owner and contact labels come from LEFT JOINs so a card can render "Alex ·
// Ada Lovelace" without the client resolving ids, and an unassigned deal still
// returns a row.
const dealColumns = `
	d.id::text, d.title, d.description, d.amount::float8, d.stage,
	d.owner_user_id::text, u.name, u.email,
	d.contact_id::text, NULLIF(concat_ws(' ', c.first_name, c.last_name), ''),
	d.account_id::text, d.expected_close_date, d.position, d.created_at, d.updated_at`

const dealFrom = `
	FROM deals d
	LEFT JOIN users u ON u.id = d.owner_user_id
	LEFT JOIN contacts c ON c.id = d.contact_id `

func (s *store) board(ctx context.Context, orgID string, limit int) ([]Deal, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+dealColumns+dealFrom+
			`WHERE d.org_id = $1
			 ORDER BY d.stage, d.position, d.id
			 LIMIT $2`, orgID, limit)
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
		`SELECT `+dealColumns+dealFrom+`WHERE d.org_id = $1 AND d.id = $2`, orgID, id))
}

// create appends the deal to the end of its column.
func (s *store) create(ctx context.Context, orgID string, in Input) (Deal, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`INSERT INTO deals
		   (org_id, title, description, amount, stage, owner_user_id, contact_id,
		    expected_close_date, position)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8,
		         COALESCE((SELECT max(position) + $9 FROM deals
		                   WHERE org_id = $1 AND stage = $5), 0))
		 RETURNING id::text`,
		orgID, in.Title, in.Description, in.Amount, in.Stage, in.OwnerUserID,
		in.ContactID, in.ExpectedCloseDate, float64(positionGap),
	).Scan(&id)
	if err != nil {
		return Deal{}, translate(err)
	}
	return s.get(ctx, orgID, id)
}

func (s *store) update(ctx context.Context, orgID, id string, in Input) (Deal, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE deals
		 SET title = $3, description = $4, amount = $5, stage = $6,
		     owner_user_id = $7, contact_id = $8, expected_close_date = $9,
		     updated_at = now()
		 WHERE org_id = $1 AND id = $2`,
		orgID, id, in.Title, in.Description, in.Amount, in.Stage,
		in.OwnerUserID, in.ContactID, in.ExpectedCloseDate)
	if err != nil {
		return Deal{}, translate(err)
	}
	if tag.RowsAffected() == 0 {
		return Deal{}, ErrNotFound
	}
	return s.get(ctx, orgID, id)
}

func (s *store) delete(ctx context.Context, orgID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM deals WHERE org_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// move places a deal at `index` within `stage` and renumbers that column.
//
// Same approach as leads: renumbering the whole destination column keeps
// positions exact forever, with no precision drift and no periodic compaction.
// One transaction, so a concurrent move can't interleave.
func (s *store) move(ctx context.Context, orgID, id, stage string, index int) (Deal, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Deal{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx,
		`UPDATE deals SET stage = $3, updated_at = now() WHERE org_id = $1 AND id = $2`,
		orgID, id, stage)
	if err != nil {
		return Deal{}, translate(err)
	}
	if tag.RowsAffected() == 0 {
		return Deal{}, ErrNotFound
	}

	rows, err := tx.Query(ctx,
		`SELECT id::text FROM deals
		 WHERE org_id = $1 AND stage = $2 AND id <> $3
		 ORDER BY position, id`, orgID, stage, id)
	if err != nil {
		return Deal{}, err
	}
	ids := make([]string, 0, 32)
	for rows.Next() {
		var rowID string
		if err := rows.Scan(&rowID); err != nil {
			rows.Close()
			return Deal{}, err
		}
		ids = append(ids, rowID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Deal{}, err
	}

	if index < 0 {
		index = 0
	}
	if index > len(ids) {
		index = len(ids)
	}
	ids = append(ids, "")
	copy(ids[index+1:], ids[index:])
	ids[index] = id

	positions := make([]float64, len(ids))
	for i := range ids {
		positions[i] = float64((i + 1) * positionGap)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE deals SET position = v.pos
		 FROM unnest($2::uuid[], $3::float8[]) AS v(id, pos)
		 WHERE deals.id = v.id AND deals.org_id = $1`,
		orgID, ids, positions); err != nil {
		return Deal{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Deal{}, err
	}
	return s.get(ctx, orgID, id)
}

// refInOrg checks a client-supplied foreign key against the caller's org. The FK
// constraint alone would accept another tenant's row.
func (s *store) refInOrg(ctx context.Context, table, orgID, id string) (bool, error) {
	// table is never user input — callers pass a literal.
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM `+table+` WHERE org_id = $1 AND id = $2)`,
		orgID, id).Scan(&exists)
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
		 FROM deals WHERE org_id = $1 GROUP BY stage`, orgID)
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
