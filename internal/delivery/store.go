package delivery

import (
	"context"
	"errors"
	"time"

	"github.com/go-crm/services/pkg/apperr"
	"github.com/go-crm/services/pkg/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
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
	t.notes, t.position,
	t.deal_id::text, dl.title AS deal_title, dl.stage AS deal_stage,
	t.updated_by::text, u.name AS updated_by_name,
	t.created_at, t.updated_at`

const rowFrom = `
	FROM delivery_tracker t
	LEFT JOIN users u ON u.id = t.updated_by
	LEFT JOIN deals dl ON dl.id = t.deal_id AND dl.deleted_at IS NULL `

// rowOrder is the user's arrangement, with creation order as the tiebreak so two
// rows that share a position never swap places between renders.
const rowOrder = ` ORDER BY t.position, t.created_at, t.id `

type scanner interface {
	Scan(dest ...any) error
}

func scanRow(s scanner) (Row, error) {
	var r Row
	// The driver knows how to read a DATE into a time.Time; Date is our own
	// wire type, so the wrapping happens here rather than in a driver hook.
	var implementationDate *time.Time

	err := s.Scan(
		&r.ID, &r.Client, &r.Products, &r.Locations, &r.TotalCameras, &r.Status,
		&implementationDate, &r.CurrentStages, &r.KeyContacts, &r.NextSteps,
		&r.Notes, &r.Position,
		&r.DealID, &r.DealTitle, &r.DealStage,
		&r.UpdatedBy, &r.UpdatedByName,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Row{}, ErrNotFound
	}
	r.ImplementationDate = dateFromPtr(implementationDate)
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
		 SELECT `+rowColumns+`
		   FROM inserted t
		   LEFT JOIN users u ON u.id = t.updated_by
		   LEFT JOIN deals dl ON dl.id = t.deal_id AND dl.deleted_at IS NULL`,
		orgID, in.Client, in.Products, in.Locations, in.TotalCameras, in.Status,
		in.ImplementationDate.timePtr(), in.CurrentStages, in.KeyContacts, in.NextSteps, in.Notes,
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
		 SELECT `+rowColumns+`
		   FROM updated t
		   LEFT JOIN users u ON u.id = t.updated_by
		   LEFT JOIN deals dl ON dl.id = t.deal_id AND dl.deleted_at IS NULL`,
		orgID, id, in.Client, in.Products, in.Locations, in.TotalCameras,
		in.Status, in.ImplementationDate.timePtr(), in.CurrentStages,
		in.KeyContacts, in.NextSteps, in.Notes, nullableID(userID),
	))
	if err != nil {
		return r, mapWriteErr(err)
	}

	// Push the three shared columns back onto the linked deal, so the deal form
	// and the board show what the tracker was just told. A row with no deal
	// updates nothing.
	if r.DealID != nil {
		if _, err := s.pool.Exec(ctx,
			`UPDATE deals
			    SET products = $2, location = $3, total_cameras = $4, updated_at = now()
			  WHERE id = $1 AND deleted_at IS NULL`,
			*r.DealID, in.Products, in.Locations, in.TotalCameras,
		); err != nil {
			return Row{}, err
		}
	}
	return r, nil
}

func (s *store) delete(ctx context.Context, orgID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM delivery_tracker WHERE org_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		// A malformed uuid is a request for a row that cannot exist, not a 500.
		if database.IsInvalidTextRepr(err) {
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
	if database.IsInvalidTextRepr(err) {
		return apperr.Invalid("one of those row ids is not valid")
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
	case database.IsUniqueViolationOn(err, clientUniqueIdxName):
		return ErrClientTaken
	case database.IsInvalidTextRepr(err):
		return ErrNotFound
	default:
		return err
	}
}

// upsertMany applies a confirmed import in one transaction: either the whole
// sheet lands or none of it does. A half-applied import is the worst outcome —
// the user cannot tell what to re-upload.
//
// Matching is by client name, resolved explicitly rather than with ON CONFLICT.
// Since tracker rows link to deals, the client name is only unique among
// *unlinked* rows — a client with two deals legitimately has two rows — so there
// is no single index for ON CONFLICT to infer. resolveByClient below encodes the
// same rule the preview shows.
//
// COALESCE on every optional column keeps the preview's promise that a blank
// cell is "no opinion": an import fills gaps and overwrites what it carries, and
// never clears a column it does not have.
func (s *store) upsertMany(ctx context.Context, orgID, userID string, rows []Input) (CommitResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CommitResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var out CommitResult
	actor := nullableID(userID)

	for _, in := range rows {
		existingID, dealID, err := resolveByClient(ctx, tx, orgID, in.Client)
		if err != nil {
			return CommitResult{}, err
		}

		if existingID == "" {
			if _, err := tx.Exec(ctx,
				`INSERT INTO delivery_tracker
				   (org_id, client, products, locations, total_cameras, status,
				    implementation_date, current_stages, key_contacts, next_steps, notes,
				    position, created_by, updated_by)
				 VALUES
				   ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
				    (SELECT COALESCE(max(position), 0) + $12 FROM delivery_tracker WHERE org_id = $1),
				    $13, $13)`,
				orgID, in.Client, in.Products, in.Locations, in.TotalCameras, in.Status,
				in.ImplementationDate.timePtr(), in.CurrentStages, in.KeyContacts,
				in.NextSteps, in.Notes, positionStep, actor,
			); err != nil {
				return CommitResult{}, mapWriteErr(err)
			}
			out.Created++
			continue
		}

		if _, err := tx.Exec(ctx,
			`UPDATE delivery_tracker SET
			   client              = $2,
			   products            = COALESCE($3, products),
			   locations           = COALESCE($4, locations),
			   total_cameras       = COALESCE($5, total_cameras),
			   status              = COALESCE($6, status),
			   implementation_date = COALESCE($7, implementation_date),
			   current_stages      = COALESCE($8, current_stages),
			   key_contacts        = COALESCE($9, key_contacts),
			   next_steps          = COALESCE($10, next_steps),
			   notes               = COALESCE($11, notes),
			   updated_by          = $12
			 WHERE id = $1`,
			existingID, in.Client, in.Products, in.Locations, in.TotalCameras,
			in.Status, in.ImplementationDate.timePtr(), in.CurrentStages,
			in.KeyContacts, in.NextSteps, in.Notes, actor,
		); err != nil {
			return CommitResult{}, mapWriteErr(err)
		}

		// A sheet that updates a linked row has to reach the deal too, or the
		// board would keep showing the camera count the import just replaced.
		if dealID != "" {
			if _, err := tx.Exec(ctx,
				`UPDATE deals SET
				   products      = COALESCE($2, products),
				   location      = COALESCE($3, location),
				   total_cameras = COALESCE($4, total_cameras),
				   updated_at    = now()
				 WHERE id = $1 AND deleted_at IS NULL`,
				dealID, in.Products, in.Locations, in.TotalCameras,
			); err != nil {
				return CommitResult{}, err
			}
		}
		out.Updated++
	}

	if err := tx.Commit(ctx); err != nil {
		return CommitResult{}, err
	}
	return out, nil
}

// resolveByClient finds the row an import line should write to, returning its id
// (empty when there is none) and the deal behind it.
//
// Unlinked rows win: those are the ones imports have always owned, and a sheet
// is a statement about a client rather than about one sale. Falling back to the
// oldest linked row means re-importing a client that has since been linked
// updates that row instead of quietly growing a duplicate beside it.
func resolveByClient(ctx context.Context, q Querier, orgID, client string) (string, string, error) {
	var id, dealID *string
	err := q.QueryRow(ctx,
		`SELECT id::text, deal_id::text
		   FROM delivery_tracker
		  WHERE org_id = $1 AND lower(btrim(client)) = lower(btrim($2))
		  ORDER BY (deal_id IS NOT NULL), created_at, id
		  LIMIT 1`,
		orgID, client).Scan(&id, &dealID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	return derefOr(id), derefOr(dealID), nil
}

func derefOr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
