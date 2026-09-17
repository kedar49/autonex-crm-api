package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-crm/services/pkg/apperr"
	"github.com/go-crm/services/pkg/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	clientUniqueIdxName = "delivery_tracker_client_idx"
	// dealUniqueIdxName enforces one tracker row per deal. A sheet naming the
	// same deal on two lines trips it.
	dealUniqueIdxName = "delivery_tracker_deal_idx"
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
	-- dl.id, not t.deal_id: the join below drops deleted deals, so reading the
	-- id off the join makes a row whose deal was retired report as unlinked
	-- rather than as linked-to-nothing. Rows orphaned before deals started
	-- unlinking on delete are covered by this too.
	dl.id::text, dl.title AS deal_title, dl.stage AS deal_stage,
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
	actor, err := knownActor(ctx, s.pool, userID)
	if err != nil {
		return Row{}, err
	}
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
		positionStep, actor,
	))
	return r, mapWriteErr(err)
}

func (s *store) update(ctx context.Context, orgID, userID, id string, in Input) (Row, error) {
	actor, err := knownActor(ctx, s.pool, userID)
	if err != nil {
		return Row{}, err
	}
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
		in.KeyContacts, in.NextSteps, in.Notes, actor,
	))
	if err != nil {
		return r, mapWriteErr(err)
	}

	// Push the shared columns back onto the linked deal, so the deal form and the
	// board show what the tracker was just told. A row with no deal updates
	// nothing, which is the case for every hand-added and imported row.
	if r.DealID != nil {
		if _, err := s.pool.Exec(ctx,
			`UPDATE deals
			    SET products = $2, location = $3, total_cameras = $4, updated_at = now()
			  WHERE id = $1 AND deleted_at IS NULL`,
			*r.DealID, in.Products, in.Locations, in.TotalCameras,
		); err != nil {
			return Row{}, err
		}

		// And move the deal to the stage the chosen label means. Guarded against
		// the labels that mean more than one stage — see SyncStageToDeal.
		if in.CurrentStages != nil {
			if _, err := SyncStageToDeal(ctx, s.pool, *r.DealID, *in.CurrentStages); err != nil {
				return Row{}, err
			}
			// The row was read back before the deal moved, so its denormalised
			// deal_stage would be one edit stale on the response the grid renders.
			if err := s.refreshDealStage(ctx, &r); err != nil {
				return Row{}, err
			}
		}
	}
	return r, nil
}

// refreshDealStage re-reads the linked deal's stage onto an already-scanned row.
//
// The update statement returns the row as it was when it was written, which is
// before the deal moved. Without this the grid would show the previous stage in
// the Deal column until the next refetch, and the user would reasonably conclude
// the sync had not worked.
func (s *store) refreshDealStage(ctx context.Context, r *Row) error {
	if r.DealID == nil {
		return nil
	}
	var stage *string
	if err := s.pool.QueryRow(ctx,
		`SELECT stage FROM deals WHERE id = $1 AND deleted_at IS NULL`,
		*r.DealID).Scan(&stage); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	r.DealStage = stage
	return nil
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

// knownActor resolves the acting user for created_by / updated_by, and returns
// NULL for one the users table does not have.
//
// Those columns are a FK to users, so an id with no row raises a foreign key
// violation — and because a whole import runs in one transaction, that took the
// entire sheet down with an opaque 500 rather than dropping one attribution. A
// caller can legitimately be unknown here: the columns are nullable and the FK
// is ON DELETE SET NULL, so "unknown author" is already a state this table is
// built to hold. Recording the rows without the name is strictly better than
// refusing the import.
//
// The lookup runs once per import, not once per row.
func knownActor(ctx context.Context, q Querier, userID string) (*string, error) {
	if userID == "" {
		return nil, nil
	}
	var id string
	err := q.QueryRow(ctx, `SELECT id::text FROM users WHERE id = $1`, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
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
	case database.IsUniqueViolationOn(err, dealUniqueIdxName):
		return ErrDealTaken
	case database.IsInvalidTextRepr(err):
		return ErrNotFound
	default:
		return err
	}
}

// rowErr names the line an import died on.
//
// A sheet is dozens of rows and the whole thing is one transaction, so a bare
// "could not apply that import" leaves the user to find the bad line by
// bisecting their own spreadsheet. Naming the row and the client turns that into
// a single edit. The index is the position in the submitted batch, which is the
// order the rows were previewed in.
//
// Constraint failures we recognize become caller-safe validation errors, since
// the fix is in the sheet. Anything else is passed through and still answered as
// a 500, because it is ours.
func rowErr(i int, in Input, err error) error {
	if err == nil {
		return nil
	}
	client := strings.TrimSpace(in.Client)
	if client == "" {
		client = "(no client)"
	}
	switch {
	case errors.Is(err, ErrClientTaken):
		return apperr.Invalid(
			"row %d (%s): another row already tracks that client at that location — remove the duplicate line and re-import",
			i+1, client)
	case errors.Is(err, ErrDealTaken):
		return apperr.Invalid(
			"row %d (%s): that deal already has a tracker row — two lines cannot point at one deal",
			i+1, client)
	case apperr.IsValidation(err):
		return apperr.Invalid("row %d (%s): %s", i+1, client, err.Error())
	default:
		return fmt.Errorf("row %d (%s): %w", i+1, client, err)
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
	actor, err := knownActor(ctx, tx, userID)
	if err != nil {
		return CommitResult{}, err
	}

	for i, in := range rows {
		existingID, dealID, err := resolveByClient(ctx, tx, orgID, in.Client, in.Locations, in.DealTitle)
		if err != nil {
			return CommitResult{}, rowErr(i, in, err)
		}

		dealIDPtr := nullableID(dealID)

		if existingID == "" {
			if _, err := tx.Exec(ctx,
				`INSERT INTO delivery_tracker
				   (org_id, deal_id, client, products, locations, total_cameras, status,
				    implementation_date, current_stages, key_contacts, next_steps, notes,
				    position, created_by, updated_by)
				 VALUES
				   ($1,
				    -- Only claim the deal if no row holds it yet. delivery_tracker_deal_idx
				    -- allows one row per deal, and an import that tried to take an already
				    -- claimed deal aborted the whole sheet with an opaque 500. Leaving the
				    -- row unlinked keeps the import's data and costs only the link, which
				    -- the next stage change or edit restores.
				    (SELECT $2::uuid WHERE $2::uuid IS NULL
				       OR NOT EXISTS (SELECT 1 FROM delivery_tracker o WHERE o.deal_id = $2::uuid)),
				    $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
				    (SELECT COALESCE(max(position), 0) + $13 FROM delivery_tracker WHERE org_id = $1),
				    $14, $14)`,
				orgID, dealIDPtr, in.Client, in.Products, in.Locations, in.TotalCameras, in.Status,
				in.ImplementationDate.timePtr(), in.CurrentStages, in.KeyContacts,
				in.NextSteps, in.Notes, positionStep, actor,
			); err != nil {
				return CommitResult{}, rowErr(i, in, mapWriteErr(err))
			}
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
			out.Created++
			continue
		}

		if _, err := tx.Exec(ctx,
			`UPDATE delivery_tracker SET
			   client              = $2,
			   deal_id             = COALESCE(
			                           deal_id,
			                           (SELECT $3::uuid WHERE $3::uuid IS NULL
			                              OR NOT EXISTS (SELECT 1 FROM delivery_tracker o
			                                              WHERE o.deal_id = $3::uuid AND o.id <> $1::uuid))),
			   products            = COALESCE($4, products),
			   locations           = COALESCE($5, locations),
			   total_cameras       = COALESCE($6, total_cameras),
			   status              = COALESCE($7, status),
			   implementation_date = COALESCE($8, implementation_date),
			   current_stages      = COALESCE($9, current_stages),
			   key_contacts        = COALESCE($10, key_contacts),
			   next_steps          = COALESCE($11, next_steps),
			   notes               = COALESCE($12, notes),
			   updated_by          = $13
			 WHERE id = $1`,
			existingID, in.Client, dealIDPtr, in.Products, in.Locations, in.TotalCameras,
			in.Status, in.ImplementationDate.timePtr(), in.CurrentStages,
			in.KeyContacts, in.NextSteps, in.Notes, actor,
		); err != nil {
			return CommitResult{}, rowErr(i, in, mapWriteErr(err))
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
func resolveByClient(ctx context.Context, q Querier, orgID, client string, location, dealTitle *string) (string, string, error) {
	var id, dealID *string

	dealStr := ""
	if dealTitle != nil {
		dealStr = strings.TrimSpace(*dealTitle)
	}
	if dealStr != "" {
		var dID string
		if err := q.QueryRow(ctx,
			`SELECT d.id::text FROM deals d WHERE d.deleted_at IS NULL AND lower(btrim(d.title)) = lower(btrim($1)) LIMIT 1`,
			dealStr).Scan(&dID); err == nil && dID != "" {
			dealID = &dID
			var tID string
			if err := q.QueryRow(ctx,
				`SELECT t.id::text FROM delivery_tracker t WHERE t.org_id = $1 AND t.deal_id = $2 LIMIT 1`,
				orgID, dID).Scan(&tID); err == nil && tID != "" {
				id = &tID
			}
		}
	}

	locStr := ""
	if location != nil {
		locStr = strings.TrimSpace(*location)
	}

	// 1. Try matching on client name and location (if provided)
	var err error
	if id != nil {
		err = nil
	} else if locStr != "" {
		err = q.QueryRow(ctx,
			`SELECT id::text, deal_id::text
			   FROM delivery_tracker
			  WHERE org_id = $1
			    AND lower(btrim(client)) = lower(btrim($2))
			    AND locations IS NOT NULL
			    AND lower(btrim(locations)) = lower(btrim($3))
			  ORDER BY (deal_id IS NOT NULL), created_at, id
			  LIMIT 1`,
			orgID, client, locStr).Scan(&id, &dealID)
	} else {
		err = pgx.ErrNoRows
	}

	// 2. Try matching on client name alone (only if locStr is empty, or existing row has no location)
	if errors.Is(err, pgx.ErrNoRows) {
		if locStr != "" {
			err = q.QueryRow(ctx,
				`SELECT id::text, deal_id::text
				   FROM delivery_tracker
				  WHERE org_id = $1
				    AND lower(btrim(client)) = lower(btrim($2))
				    AND (locations IS NULL OR btrim(locations) = '')
				  ORDER BY (deal_id IS NOT NULL), created_at, id
				  LIMIT 1`,
				orgID, client).Scan(&id, &dealID)
		} else {
			err = q.QueryRow(ctx,
				`SELECT id::text, deal_id::text
				   FROM delivery_tracker
				  WHERE org_id = $1 AND lower(btrim(client)) = lower(btrim($2))
				  ORDER BY (deal_id IS NOT NULL), created_at, id
				  LIMIT 1`,
				orgID, client).Scan(&id, &dealID)
		}
	}

	// 3. Fallback: normalize non-alphanumeric characters generically
	if errors.Is(err, pgx.ErrNoRows) {
		cleanedClient := cleanIdentifier(client)
		if cleanedClient != "" {
			if locStr != "" {
				err = q.QueryRow(ctx,
					`SELECT id::text, deal_id::text
					   FROM delivery_tracker
					  WHERE org_id = $1
					    AND regexp_replace(lower(client), '[^a-z0-9]', '', 'g') = $2
					    AND (locations IS NULL OR btrim(locations) = '' OR lower(btrim(locations)) = lower(btrim($3)))
					  ORDER BY (deal_id IS NOT NULL), created_at, id
					  LIMIT 1`,
					orgID, cleanedClient, locStr).Scan(&id, &dealID)
			} else {
				err = q.QueryRow(ctx,
					`SELECT id::text, deal_id::text
					   FROM delivery_tracker
					  WHERE org_id = $1 AND regexp_replace(lower(client), '[^a-z0-9]', '', 'g') = $2
					  ORDER BY (deal_id IS NOT NULL), created_at, id
					  LIMIT 1`,
					orgID, cleanedClient).Scan(&id, &dealID)
			}
		}
	}

	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", "", err
	}

	resID := derefOr(id)
	resDealID := derefOr(dealID)

	// If row has no linked deal yet, check if an existing deal matches this client and location
	if resDealID == "" {
		var foundDealID string
		var dErr error
		if locStr != "" {
			dErr = q.QueryRow(ctx,
				`SELECT d.id::text
				   FROM deals d
				   LEFT JOIN accounts a ON a.id = d.account_id
				  WHERE d.deleted_at IS NULL
				    AND (lower(btrim(d.title)) = lower(btrim($1)) OR (a.name IS NOT NULL AND lower(btrim(a.name)) = lower(btrim($1))))
				    AND d.location IS NOT NULL
				    AND lower(btrim(d.location)) = lower(btrim($2))
				  ORDER BY d.created_at DESC
				  LIMIT 1`, client, locStr).Scan(&foundDealID)
			if foundDealID == "" {
				dErr = q.QueryRow(ctx,
					`SELECT d.id::text
					   FROM deals d
					   LEFT JOIN accounts a ON a.id = d.account_id
					  WHERE d.deleted_at IS NULL
					    AND (lower(btrim(d.title)) = lower(btrim($1)) OR (a.name IS NOT NULL AND lower(btrim(a.name)) = lower(btrim($1))))
					    AND (d.location IS NULL OR btrim(d.location) = '')
					  ORDER BY d.created_at DESC
					  LIMIT 1`, client).Scan(&foundDealID)
			}
		} else {
			dErr = q.QueryRow(ctx,
				`SELECT d.id::text
				   FROM deals d
				   LEFT JOIN accounts a ON a.id = d.account_id
				  WHERE d.deleted_at IS NULL
				    AND (lower(btrim(d.title)) = lower(btrim($1)) OR (a.name IS NOT NULL AND lower(btrim(a.name)) = lower(btrim($1))))
				  ORDER BY d.created_at DESC
				  LIMIT 1`, client).Scan(&foundDealID)
		}
		if dErr == nil && foundDealID != "" {
			resDealID = foundDealID
		}
	}

	return resID, resDealID, nil
}

func cleanIdentifier(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func derefOr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
