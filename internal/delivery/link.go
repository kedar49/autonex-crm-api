package delivery

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is the slice of pgx both a pool and a transaction satisfy, so the
// deals module can keep these calls inside whatever transaction it is already
// running.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// DealFields is the slice of a deal the tracker mirrors.
//
// These three columns exist on both tables and are kept in step in both
// directions: the deal form and the tracker grid are two views of the same
// answer to "what are we installing, where, and how many cameras". A write to
// either side pushes to the other in the same statement, so there is no window
// where they disagree.
//
// Two-way means last write wins. That is a real (if narrow) race — two people
// editing the same deal from the two screens at once — and the alternative,
// making one side read-only, was considered and rejected.
type DealFields struct {
	Products     *string
	Location     *string
	TotalCameras *int
}

// SyncFromDeal pushes a deal's deployment fields onto its linked tracker row.
// A deal with no tracker row is a no-op, which is the common case: most deals
// never reach delivery.
func SyncFromDeal(ctx context.Context, q Querier, dealID string, f DealFields) error {
	_, err := q.Exec(ctx,
		`UPDATE delivery_tracker
		    SET products = $2, locations = $3, total_cameras = $4
		  WHERE deal_id = $1`,
		dealID, f.Products, f.Location, f.TotalCameras)
	return err
}

// EnsureRowForDeal gives a deal a tracker row, and returns whether it made one.
//
// Called when a deal reaches the delivery stage. Three cases, in order:
//
//  1. The deal already has a row — nothing to do.
//  2. An unlinked row already names this client — adopt it rather than creating
//     a duplicate. This is what makes a sheet imported before the deal existed
//     turn into that deal's row the moment it reaches delivery.
//  3. Otherwise create one, seeded from the deal.
//
// The client name comes from the account, falling back to the deal title: a
// tracker row is filed under who it is for, and a deal without an account still
// has to land somewhere findable.
func EnsureRowForDeal(ctx context.Context, q Querier, orgID, dealID, userID string) (bool, error) {
	var created bool
	err := q.QueryRow(ctx,
		`WITH deal AS (
		   SELECT d.id,
		          COALESCE(NULLIF(btrim(a.name), ''), d.title, 'Untitled') AS client,
		          d.products, d.location, d.total_cameras
		     FROM deals d
		     LEFT JOIN accounts a ON a.id = d.account_id
		    WHERE d.id = $1 AND d.deleted_at IS NULL
		 ),
		 adopted AS (
		   UPDATE delivery_tracker t
		      SET deal_id       = deal.id,
		          products      = COALESCE(deal.products, t.products),
		          locations     = COALESCE(deal.location, t.locations),
		          total_cameras = COALESCE(deal.total_cameras, t.total_cameras),
		          updated_by    = $3
		     FROM deal
		    WHERE t.org_id = $2
		      AND t.deal_id IS NULL
		      AND lower(btrim(t.client)) = lower(btrim(deal.client))
		      -- Only when the deal has no row of its own yet.
		      AND NOT EXISTS (SELECT 1 FROM delivery_tracker x WHERE x.deal_id = deal.id)
		   RETURNING t.id
		 ),
		 inserted AS (
		   INSERT INTO delivery_tracker
		     (org_id, deal_id, client, products, locations, total_cameras,
		      position, created_by, updated_by)
		   SELECT $2, deal.id, deal.client, deal.products, deal.location,
		          deal.total_cameras,
		          (SELECT COALESCE(max(position), 0) + 1000
		             FROM delivery_tracker WHERE org_id = $2),
		          $3, $3
		     FROM deal
		    WHERE NOT EXISTS (SELECT 1 FROM adopted)
		      AND NOT EXISTS (SELECT 1 FROM delivery_tracker x WHERE x.deal_id = deal.id)
		   RETURNING id
		 )
		 SELECT EXISTS (SELECT 1 FROM inserted) OR EXISTS (SELECT 1 FROM adopted)`,
		dealID, orgID, nullableID(userID),
	).Scan(&created)
	return created, err
}
