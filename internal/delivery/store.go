package delivery

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	pgInvalidTextRepr   = "22P02"
	pgUniqueViolation   = "23505"
	clientUniqueIdxName = "delivery_tracker_client_idx"
)

type store struct {
	pool *pgxpool.Pool
}

// rowColumns is the read shape. The updater's name is joined in so the table can
// show who last touched a line without a second request per row.
const rowColumns = `
	t.id::text, t.client, t.products, t.locations, t.total_cameras, t.status,
	t.implementation_date, t.current_stages, t.key_contacts, t.next_steps,
	t.notes, t.position, t.updated_by::text, u.name AS updated_by_name,
	t.created_at, t.updated_at`

const rowFrom = ` FROM delivery_tracker t LEFT JOIN users u ON u.id = t.updated_by `

// rowOrder is the user's arrangement, with creation order as the tiebreak so two
// rows that share a position never swap places between renders.
const rowOrder = ` ORDER BY t.position, t.created_at, t.id `

type scanner interface {
	Scan(dest ...any) error
}

func scanRow(s scanner) (Row, error) {
	var r Row
	err := s.Scan(
		&r.ID, &r.Client, &r.Products, &r.Locations, &r.TotalCameras, &r.Status,
		&r.ImplementationDate, &r.CurrentStages, &r.KeyContacts, &r.NextSteps,
		&r.Notes, &r.Position, &r.UpdatedBy, &r.UpdatedByName,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Row{}, ErrNotFound
	}
	return r, err
}

func (s *store) list(ctx context.Context, orgID string, limit int) ([]Row, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+rowColumns+rowFrom+`WHERE t.org_id = $1`+rowOrder+`LIMIT $2`,
		orgID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Non-nil so an empty tracker marshals as [] rather than null.
	out := make([]Row, 0, 64)
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// create appends the row. The position is derived in SQL rather than read first
// and written second, so two people adding a row at once cannot land on the same
// slot.
func (s *store) create(ctx context.Context, orgID, userID string, in Input) (Row, error) {
	r, err := scanRow(s.pool.QueryRow(ctx,
		`WITH inserted AS (
		   INSERT INTO delivery_tracker
		     (org_id, client, products, locations, total_cameras, status,
		      implementation_date, current_stages, key_contacts, next_steps, notes,
		      position, created_by, updated_by)
		   VALUES
		     ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
		      (SELECT COALESCE(max(position), 0) + $12 FROM delivery_tracker WHERE org_id = $1),
		      $13, $13)
		   RETURNING *
		 )
		 SELECT `+rowColumns+` FROM inserted t LEFT JOIN users u ON u.id = t.updated_by`,
		orgID, in.Client, in.Products, in.Locations, in.TotalCameras, in.Status,
		in.ImplementationDate, in.CurrentStages, in.KeyContacts, in.NextSteps, in.Notes,
		positionStep, nullableID(userID),
	))
	return r, mapWriteErr(err)
}

func (s *store) update(ctx context.Context, orgID, userID, id string, in Input) (Row, error) {
	r, err := scanRow(s.pool.QueryRow(ctx,
		`WITH updated AS (
		   UPDATE delivery_tracker
		      SET client = $3, products = $4, locations = $5, total_cameras = $6,
		          status = $7, implementation_date = $8, current_stages = $9,
		          key_contacts = $10, next_steps = $11, notes = $12, updated_by = $13
		    WHERE org_id = $1 AND id = $2
		   RETURNING *
		 )
		 SELECT `+rowColumns+` FROM updated t LEFT JOIN users u ON u.id = t.updated_by`,
		orgID, id, in.Client, in.Products, in.Locations, in.TotalCameras,
		in.Status, in.ImplementationDate, in.CurrentStages,
		in.KeyContacts, in.NextSteps, in.Notes, nullableID(userID),
	))
	return r, mapWriteErr(err)
}

func (s *store) delete(ctx context.Context, orgID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM delivery_tracker WHERE org_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		// A malformed uuid is a request for a row that cannot exist, not a 500.
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

// reorder rewrites positions from the client's ordering in one statement.
//
// The org_id predicate is what keeps this safe: a caller can send any id it
// likes, and only its own rows move.
func (s *store) reorder(ctx context.Context, orgID string, ids []string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE delivery_tracker t
		    SET position = o.ord * $3
		   FROM unnest($2::uuid[]) WITH ORDINALITY AS o(id, ord)
		  WHERE t.id = o.id AND t.org_id = $1`,
		orgID, ids, positionStep)
	if isPgCode(err, pgInvalidTextRepr) {
		return invalid("one of those row ids is not valid")
	}
	return err
}

// nullableID turns an empty actor id into a SQL NULL. The column is a FK to
// users, so "" would fail the cast rather than record "unknown".
func nullableID(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}

// mapWriteErr translates the two Postgres errors a write can raise into domain
// errors, so handlers answer 409/404 instead of 500.
func mapWriteErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return ErrNotFound
	case isUniqueViolation(err, clientUniqueIdxName):
		return ErrClientTaken
	case isPgCode(err, pgInvalidTextRepr):
		return ErrNotFound
	default:
		return err
	}
}

func isPgCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == pgUniqueViolation &&
		pgErr.ConstraintName == constraint
}

// upsertMany applies a confirmed import in one transaction: either the whole
// sheet lands or none of it does. A half-applied import is the worst outcome —
// the user cannot tell what to re-upload.
//
// The conflict target is the unique index on (org_id, lower(btrim(client))),
// which is what makes "upsert by client name" mean the same thing here as it did
// in the preview.
//
// COALESCE on every optional column implements the preview's promise that a
// blank cell is "no opinion": an import fills gaps and overwrites what it
// carries, and never clears a column it does not have.
func (s *store) upsertMany(ctx context.Context, orgID, userID string, rows []Input) (CommitResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CommitResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var out CommitResult
	actor := nullableID(userID)

	for _, in := range rows {
		var inserted bool
		err := tx.QueryRow(ctx,
			`INSERT INTO delivery_tracker
			   (org_id, client, products, locations, total_cameras, status,
			    implementation_date, current_stages, key_contacts, next_steps, notes,
			    position, created_by, updated_by)
			 VALUES
			   ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
			    (SELECT COALESCE(max(position), 0) + $12 FROM delivery_tracker WHERE org_id = $1),
			    $13, $13)
			 ON CONFLICT (org_id, lower(btrim(client))) DO UPDATE SET
			   client              = EXCLUDED.client,
			   products            = COALESCE(EXCLUDED.products, delivery_tracker.products),
			   locations           = COALESCE(EXCLUDED.locations, delivery_tracker.locations),
			   total_cameras       = COALESCE(EXCLUDED.total_cameras, delivery_tracker.total_cameras),
			   status              = COALESCE(EXCLUDED.status, delivery_tracker.status),
			   implementation_date = COALESCE(EXCLUDED.implementation_date, delivery_tracker.implementation_date),
			   current_stages      = COALESCE(EXCLUDED.current_stages, delivery_tracker.current_stages),
			   key_contacts        = COALESCE(EXCLUDED.key_contacts, delivery_tracker.key_contacts),
			   next_steps          = COALESCE(EXCLUDED.next_steps, delivery_tracker.next_steps),
			   notes               = COALESCE(EXCLUDED.notes, delivery_tracker.notes),
			   updated_by          = EXCLUDED.updated_by
			 RETURNING (xmax = 0) AS inserted`,
			orgID, in.Client, in.Products, in.Locations, in.TotalCameras, in.Status,
			in.ImplementationDate, in.CurrentStages, in.KeyContacts, in.NextSteps, in.Notes,
			positionStep, actor,
		).Scan(&inserted)
		if err != nil {
			return CommitResult{}, mapWriteErr(err)
		}
		if inserted {
			out.Created++
		} else {
			out.Updated++
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return CommitResult{}, err
	}
	return out, nil
}
