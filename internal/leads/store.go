package leads

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound means no lead with that id exists in the caller's org. A lead
	// belonging to another tenant is indistinguishable from a missing one.
	ErrNotFound = errors.New("lead not found")
	// ErrOwnerNotFound means the assignee is not a member of the caller's org.
	ErrOwnerNotFound = errors.New("owner is not a member of this organization")
)

const (
	pgInvalidTextRepr     = "22P02" // e.g. "abc" where a UUID is expected
	pgCheckViolation      = "23514" // the stage CHECK constraint
	pgForeignKeyViolation = "23503" // owner_user_id pointing at nothing
)

// positionGap is the spacing between cards in a column. Positions are rewritten
// as multiples of this on every move, so gaps never need splitting.
const positionGap = 1000

// Lead is the leads module's view of a row, including the denormalized owner
// fields the board renders on each card.
type Lead struct {
	ID          string    `json:"id"`
	FirstName   string    `json:"firstName"`
	LastName    *string   `json:"lastName"`
	Email       *string   `json:"email"`
	Phone       *string   `json:"phone"`
	Company     *string   `json:"company"`
	Source      *string   `json:"source"`
	Notes       *string   `json:"notes"`
	Value       *float64  `json:"value"`
	Stage       string    `json:"stage"`
	OwnerUserID *string   `json:"ownerUserId"`
	OwnerName   *string   `json:"ownerName"`
	OwnerEmail  *string   `json:"ownerEmail"`
	Position    float64   `json:"position"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type store struct {
	pool *pgxpool.Pool
}

// Owner columns come from a LEFT JOIN so a card can render "Alex" without the
// client resolving user ids, and an unassigned lead still returns a row.
const leadColumns = `
	l.id::text, l.first_name, l.last_name, l.email, l.phone, l.company, l.source,
	l.notes, l.value, l.stage, l.owner_user_id::text, u.name, u.email,
	l.position, l.created_at, l.updated_at`

const leadFrom = ` FROM leads l LEFT JOIN users u ON u.id = l.owner_user_id `

// board returns every lead in the org, ordered so the client can slice it into
// columns directly. The cap is a safety valve, not paging: a kanban shows the
// whole pipeline.
func (s *store) board(ctx context.Context, orgID string, limit int) ([]Lead, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+leadColumns+leadFrom+
			`WHERE l.org_id = $1
			 ORDER BY l.stage, l.position, l.id
			 LIMIT $2`, orgID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Lead, 0, 64)
	for rows.Next() {
		l, err := scanLead(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *store) get(ctx context.Context, orgID, id string) (Lead, error) {
	return scanLead(s.pool.QueryRow(ctx,
		`SELECT `+leadColumns+leadFrom+`WHERE l.org_id = $1 AND l.id = $2`, orgID, id))
}

// create appends the lead to the end of its column.
func (s *store) create(ctx context.Context, orgID string, in Input) (Lead, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`INSERT INTO leads
		   (org_id, first_name, last_name, email, phone, company, source, notes,
		    value, stage, owner_user_id, position)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
		         COALESCE((SELECT max(position) + $12 FROM leads
		                   WHERE org_id = $1 AND stage = $10), 0))
		 RETURNING id::text`,
		orgID, in.FirstName, in.LastName, in.Email, in.Phone, in.Company, in.Source,
		in.Notes, in.Value, in.Stage, in.OwnerUserID, float64(positionGap),
	).Scan(&id)
	if err != nil {
		return Lead{}, translate(err)
	}
	return s.get(ctx, orgID, id)
}

func (s *store) update(ctx context.Context, orgID, id string, in Input) (Lead, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE leads
		 SET first_name = $3, last_name = $4, email = $5, phone = $6, company = $7,
		     source = $8, notes = $9, value = $10, stage = $11, owner_user_id = $12,
		     updated_at = now()
		 WHERE org_id = $1 AND id = $2`,
		orgID, id, in.FirstName, in.LastName, in.Email, in.Phone, in.Company,
		in.Source, in.Notes, in.Value, in.Stage, in.OwnerUserID)
	if err != nil {
		return Lead{}, translate(err)
	}
	if tag.RowsAffected() == 0 {
		return Lead{}, ErrNotFound
	}
	return s.get(ctx, orgID, id)
}

func (s *store) delete(ctx context.Context, orgID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM leads WHERE org_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// move places a lead at `index` within `stage` and renumbers that column.
//
// Renumbering the whole destination column (rather than fractional indexing
// between neighbours) keeps positions exact forever: no precision drift, no
// periodic compaction, and a column is small enough that one UPDATE covers it.
// The whole thing runs in a transaction so a concurrent move can't interleave.
func (s *store) move(ctx context.Context, orgID, id, stage string, index int) (Lead, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Lead{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Claim the lead and set its stage first, so the ordering read below sees it
	// in the destination column.
	tag, err := tx.Exec(ctx,
		`UPDATE leads SET stage = $3, updated_at = now() WHERE org_id = $1 AND id = $2`,
		orgID, id, stage)
	if err != nil {
		return Lead{}, translate(err)
	}
	if tag.RowsAffected() == 0 {
		return Lead{}, ErrNotFound
	}

	// Current order of the destination column, excluding the moved card.
	rows, err := tx.Query(ctx,
		`SELECT id::text FROM leads
		 WHERE org_id = $1 AND stage = $2 AND id <> $3
		 ORDER BY position, id`, orgID, stage, id)
	if err != nil {
		return Lead{}, err
	}
	ids := make([]string, 0, 32)
	for rows.Next() {
		var rowID string
		if err := rows.Scan(&rowID); err != nil {
			rows.Close()
			return Lead{}, err
		}
		ids = append(ids, rowID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Lead{}, err
	}

	// Insert the moved card at the requested index (clamped to the column).
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
		`UPDATE leads SET position = v.pos
		 FROM unnest($2::uuid[], $3::float8[]) AS v(id, pos)
		 WHERE leads.id = v.id AND leads.org_id = $1`,
		orgID, ids, positions); err != nil {
		return Lead{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Lead{}, err
	}
	return s.get(ctx, orgID, id)
}

// ownerInOrg reports whether the user is a member of the caller's org. Without
// this a client could assign a lead to a user in another tenant.
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

// Stats backs the dashboard: per-stage counts and value in one pass.
type Stats struct {
	Stage string  `json:"stage"`
	Count int     `json:"count"`
	Value float64 `json:"value"`
}

func (s *store) stats(ctx context.Context, orgID string) ([]Stats, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT stage, count(*), COALESCE(sum(value), 0)::float8
		 FROM leads WHERE org_id = $1 GROUP BY stage`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Stats, 0, len(Stages))
	for rows.Next() {
		var st Stats
		if err := rows.Scan(&st.Stage, &st.Count, &st.Value); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanLead(row rowScanner) (Lead, error) {
	var l Lead
	err := row.Scan(
		&l.ID, &l.FirstName, &l.LastName, &l.Email, &l.Phone, &l.Company, &l.Source,
		&l.Notes, &l.Value, &l.Stage, &l.OwnerUserID, &l.OwnerName, &l.OwnerEmail,
		&l.Position, &l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		return Lead{}, translate(err)
	}
	return l, nil
}

// translate maps pgx/Postgres failures onto the module's domain errors.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows), isPgCode(err, pgInvalidTextRepr):
		return ErrNotFound
	case isPgCode(err, pgForeignKeyViolation):
		return ErrOwnerNotFound
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
