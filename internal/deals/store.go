package deals

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Autonex009/autonex-crm-api/pkg/database"
	"github.com/jackc/pgx/v5"

	"github.com/Autonex009/autonex-crm-api/internal/delivery"
	"github.com/Autonex009/autonex-crm-api/pkg/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound means no deal with that id exists.
	ErrNotFound = errors.New("deal not found")
	// ErrRefNotFound means a referenced owner or contact doesn't exist.
	ErrRefNotFound = errors.New("referenced record is not in this organization")
)

// Deal is the module's view of a row, including the denormalized owner and
// contact labels the board renders on each card.
type Deal struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Description *string `json:"description"`
	Amount      float64 `json:"amount"`
	Stage       string  `json:"stage"`
	OwnerUserID *string `json:"ownerUserId"`
	OwnerName   *string `json:"ownerName"`
	OwnerEmail  *string `json:"ownerEmail"`
	ContactID   *string `json:"contactId"`
	ContactName *string `json:"contactName"`
	AccountID   *string `json:"accountId"`
	// The client's name, denormalized so a card can fall back to it when the
	// deal has no title of its own.
	AccountName *string `json:"accountName"`
	// The lead this deal was converted from. Read and written: it used to be set
	// on create and never selected or updated, so the edit form showed it empty
	// and saving silently dropped the link.
	LeadID   *string `json:"leadId"`
	LeadName *string `json:"leadName"`
	// What is being deployed on this deal. Free text for products and location:
	// the catalogue is not modelled, and a site list is rarely one tidy value.
	TotalCameras      *int       `json:"totalCameras"`
	Location          *string    `json:"location"`
	Products          *string    `json:"products"`
	ExpectedCloseDate *time.Time `json:"expectedCloseDate"`
	Position          float64    `json:"position"`
	Remark            *string    `json:"remark"`
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
	d.account_id::text         AS account_id,
	a.name                     AS account_name,
	d.lead_id::text            AS lead_id,
	l.contact_name             AS lead_name,
	d.total_cameras, d.location, d.products,
	d.expected_close_date,
	(row_number() OVER (PARTITION BY d.stage ORDER BY d.created_at, d.id) * 1000)::float8,
	d.created_at, d.updated_at`

const dealFrom = `
	FROM deals d
	LEFT JOIN profiles p ON p.id = d.owner_id
	LEFT JOIN contacts c ON c.id = d.primary_contact_id
	LEFT JOIN leads    l ON l.id = d.lead_id
	LEFT JOIN accounts a ON a.id = d.account_id `

func (s *store) board(ctx context.Context, _ string, limit int) ([]Deal, error) {
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

func (s *store) get(ctx context.Context, _ string, id string) (Deal, error) {
	return scanDeal(s.pool.QueryRow(ctx,
		`SELECT `+dealColumns+dealFrom+`WHERE d.id = $1 AND d.deleted_at IS NULL`, id))
}

// create adds the deal to its stage. The account link is account_id: an account
// is a company in this schema, so the accountId the client sends is stored
// there.
func (s *store) create(ctx context.Context, orgID string, in Input) (Deal, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`INSERT INTO deals
		   (title, notes, amount, stage, owner_id, primary_contact_id,
		    account_id, lead_id, expected_close_date,
		    total_cameras, location, products)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 RETURNING id::text`,
		in.Title, in.Description, in.Amount, in.Stage, in.OwnerUserID,
		in.ContactID, in.AccountID, in.LeadID, in.ExpectedCloseDate,
		in.TotalCameras, in.Location, in.Products,
	).Scan(&id)
	if err != nil {
		return Deal{}, translate(err)
	}
	d, err := s.get(ctx, orgID, id)
	if err != nil {
		return Deal{}, err
	}
	s.markLeadConverted(ctx, in.LeadID)
	return d, s.syncDelivery(ctx, orgID, d)
}

// markLeadConverted moves a linked lead to the converted stage.
//
// Attaching a lead to a deal is the lead becoming a deal, whichever screen it
// happened on. Only the Convert action used to say so, which left the list with
// two disagreeing answers to "is this converted?": the stage pill and the tab
// counts read the lead's own status, while the deal link, the dialog's
// Converted banner and the delete warning read "does a deal point at this
// lead?". A deal created from the board satisfied the second and not the first,
// so the lead sat at New while the rest of the screen called it converted, and
// the Converted tab's count disagreed with the rows that looked converted.
//
// Best-effort on purpose: the deal is already written, and failing the request
// now would report a deal that exists as an error. A lead left behind shows the
// old mismatch, which the backfill in migration 000022 clears, rather than
// costing someone the deal they just created.
func (s *store) markLeadConverted(ctx context.Context, leadID *string) {
	if leadID == nil || strings.TrimSpace(*leadID) == "" {
		return
	}
	_, _ = s.pool.Exec(ctx,
		`UPDATE leads
		    SET status = 'converted', next_follow_up_date = NULL, updated_at = now()
		  WHERE id = $1 AND deleted_at IS NULL AND status <> 'converted'`,
		*leadID)
}

func (s *store) update(ctx context.Context, orgID, id string, in Input) (Deal, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE deals
		 SET title = $2, notes = $3, amount = $4, stage = $5,
		     owner_id = $6, primary_contact_id = $7, account_id = $8,
		     expected_close_date = $9, total_cameras = $10, location = $11,
		     products = $12, lead_id = $13, updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`,
		id, in.Title, in.Description, in.Amount, in.Stage,
		in.OwnerUserID, in.ContactID, in.AccountID, in.ExpectedCloseDate,
		in.TotalCameras, in.Location, in.Products, in.LeadID)
	if err != nil {
		return Deal{}, translate(err)
	}
	if tag.RowsAffected() == 0 {
		return Deal{}, ErrNotFound
	}
	d, err := s.get(ctx, orgID, id)
	if err != nil {
		return Deal{}, err
	}
	s.markLeadConverted(ctx, in.LeadID)
	return d, s.syncDelivery(ctx, orgID, d)
}

// delete retires a deal rather than erasing it.
//
// A hard delete could not work: quotes, calendar events and slack_channels all
// reference deals with no ON DELETE clause, so Postgres refused the delete for
// any deal that had ever been quoted — and the violation surfaced as "that owner
// or contact is not part of your organization", which explains nothing.
//
// Soft delete is also the honest model. A quote or invoice raised from a deal is
// a financial record; it must keep pointing at where it came from. Every read in
// this module already filters deleted_at, so a retired deal leaves the board.
//
// The tracker row is unlinked in the same transaction rather than left behind.
// A row still pointing at a retired deal reads as linked to every query that
// only checks deal_id, while the joins that do filter deleted_at return no title
// and no stage — so the grid drew a deal column that was neither empty nor
// filled in. Unlinking keeps the row (the installation is still real) and makes
// it adoptable again by whatever deal replaces this one.
func (s *store) delete(ctx context.Context, _ string, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx,
		`UPDATE deals SET deleted_at = now(), updated_at = now()
		  WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	if err := delivery.UnlinkDeal(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// move changes which column a card sits in.
//
// The original implementation also renumbered a position column to honour the
// drop index. This schema has no such column, so the index is accepted and
// ignored: dragging a card between columns persists, dragging it within one
// does not survive a reload. Adding a position column is the only way to change
// that, which would be a schema change rather than a code one.
// The previous stage is returned alongside the moved deal so callers can say
// what changed. The CTE reads it in the same statement as the write, which is
// both one round trip and immune to another move landing in between.
func (s *store) move(ctx context.Context, orgID, id, stage string, _ int) (Deal, string, error) {
	var previous string
	err := s.pool.QueryRow(ctx,
		`WITH prev AS (SELECT stage FROM deals WHERE id = $1)
		 UPDATE deals d
		    SET stage = $2, updated_at = now()
		   FROM prev
		  WHERE d.id = $1 AND d.deleted_at IS NULL
		 RETURNING prev.stage`, id, stage).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return Deal{}, "", ErrNotFound
	}
	if err != nil {
		return Deal{}, "", translate(err)
	}
	d, err := s.get(ctx, orgID, id)
	if err != nil {
		return Deal{}, "", err
	}
	// Dragging a card into Delivery is the gesture that puts it on the tracker.
	return d, previous, s.syncDelivery(ctx, orgID, d)
}

// leadBelongsToAccount reports whether a lead is filed under the given account.
//
// A lead with no account at all passes: those are leads captured before the
// company record existed, and refusing to attach one would block the ordinary
// convert-a-lead-into-a-deal path.
func (s *store) leadBelongsToAccount(ctx context.Context, leadID, accountID string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT l.account_id IS NULL OR l.account_id = $2
		   FROM leads l WHERE l.id = $1 AND l.deleted_at IS NULL`,
		leadID, accountID).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if database.IsInvalidTextRepr(err) {
		return false, nil
	}
	return ok, err
}

// syncDelivery keeps the tracker in step with a deal that has just been written.
//
// Two things happen here, in this order: the deal gets its tracker row (created,
// or adopted from a matching unlinked one), and then the shared columns —
// products, location, cameras, and now the stage — are pushed onto it.
//
// The row is ensured at every stage, not just delivery. That is what makes the
// tracker's "Current Stage(s)" column a live mirror of the board: a column that
// only started tracking once a deal was already being installed would be blank
// for the part of the pipeline anyone actually wants to watch.
//
// Failures are returned rather than swallowed: a deal whose tracker row silently
// failed to appear is exactly the kind of gap this linking was asked for.
// The acting user is read from the request context rather than threaded through
// Create/Update/Move: it is only needed for the tracker's created_by/updated_by
// audit columns, and adding a parameter to every signature between the handler
// and here would be a lot of churn for that. A background caller with no user on
// the context records NULL, which is accurate.
func (s *store) syncDelivery(ctx context.Context, orgID string, d Deal) error {
	userID := middleware.UserID(ctx)
	if _, err := delivery.EnsureRowForDeal(ctx, s.pool, orgID, d.ID, userID); err != nil {
		return err
	}
	return delivery.SyncFromDeal(ctx, s.pool, d.ID, delivery.DealFields{
		Products:     d.Products,
		Location:     d.Location,
		TotalCameras: d.TotalCameras,
		Stage:        &d.Stage,
	})
}

// refInOrg checks a client-supplied foreign key. Single-tenant here, so
// existence is the only thing left to verify.
func (s *store) refInOrg(ctx context.Context, table, _, id string) (bool, error) {
	if table == "users" || table == "profiles" {
		var exists bool
		err := s.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM users WHERE id = $1) OR EXISTS (SELECT 1 FROM profiles WHERE id = $1)`, id).Scan(&exists)
		if err != nil {
			if database.IsInvalidTextRepr(err) {
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
		if database.IsInvalidTextRepr(err) {
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

func (s *store) stats(ctx context.Context, _ string) ([]Stats, error) {
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
		&d.AccountID, &d.AccountName, &d.LeadID, &d.LeadName, &d.TotalCameras, &d.Location, &d.Products,
		&d.ExpectedCloseDate, &d.Position, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return Deal{}, translate(err)
	}
	d.Remark = d.Description
	return d, nil
}

// translate maps pgx/Postgres failures onto the module's domain errors.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows), database.IsInvalidTextRepr(err):
		return ErrNotFound
	case database.IsForeignKeyViolation(err):
		return ErrRefNotFound
	case database.IsCheckViolation(err):
		// Only the stage CHECK can fire here; the service validates stages
		// first, so this is the belt to that braces.
		return ErrNotFound
	default:
		return err
	}
}
