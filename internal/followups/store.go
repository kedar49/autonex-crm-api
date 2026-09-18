// Package followups is the backend for the Actions dashboard: a plain,
// manually-worked list of things to do, each optionally tied to a client.
//
// The table predates this feature under a different name — it was built for
// an automated reminder sequence that never shipped, and sat unmounted until
// this repurposing. Hand-written pgx from the start, matching every other
// module's settled convention; there is no sqlc layer here to drift from.
package followups

import (
	"context"
	"errors"
	"time"

	"github.com/Autonex009/autonex-crm-api/pkg/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound means no action with that id exists in the caller's org.
	ErrNotFound = errors.New("action not found")
	// ErrAccountNotFound means the referenced account is missing or belongs to
	// another org.
	ErrAccountNotFound = errors.New("account not found")
	// ErrAssigneeNotFound means the referenced assignee isn't a member of the
	// caller's org.
	ErrAssigneeNotFound = errors.New("assignee not found")
	// ErrLeadNotFound means the referenced lead is missing or belongs to
	// another org.
	ErrLeadNotFound = errors.New("lead not found")

	// ErrLeadAccountMismatch means the action pairs a lead with an account the
	// lead does not belong to. The dialog can't produce this, but a direct API
	// call can, and it would file the action under the wrong client.
	ErrLeadAccountMismatch = errors.New("lead belongs to a different account")

	// ErrDealNotFound means the referenced deal is missing or deleted.
	ErrDealNotFound = errors.New("deal not found")
)

// Action is one row of the Actions dashboard.
type Action struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	DueAt       time.Time  `json:"dueAt"`
	Status      string     `json:"status"`
	Priority    string     `json:"priority"`
	AssignedTo  *string    `json:"assignedTo"`
	AccountID   *string    `json:"accountId"`
	LeadID      *string    `json:"leadId"`
	DealID      *string    `json:"dealId"`
	CompletedAt *time.Time `json:"completedAt"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type store struct {
	pool *pgxpool.Pool
}

const actionColumns = `id::text, title, due_at, status, priority, assigned_to::text, account_id::text,
	lead_id::text, deal_id::text, completed_at, created_at, updated_at`

// Filter narrows the org's action list; every field is optional (a zero value
// means "no opinion"), and an empty filter returns the org's whole list.
type Filter struct {
	AccountID  string
	LeadID     string
	DealID     string
	Status     string
	AssignedTo string
	DueBefore  *time.Time
	DueAfter   *time.Time
	// ExcludeDone drops completed actions. It is independent of Status so the
	// dashboard can ask for "everything still outstanding" in one query.
	ExcludeDone bool
}

func (s *store) list(ctx context.Context, orgID string, f Filter) ([]Action, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+actionColumns+`
		 FROM follow_ups
		 WHERE org_id = $1
		   AND ($2 = '' OR account_id = NULLIF($2, '')::uuid)
		   AND ($3 = '' OR status = $3)
		   AND ($4::timestamptz IS NULL OR due_at <= $4)
		   AND ($5::timestamptz IS NULL OR due_at >= $5)
		   AND ($6 = '' OR assigned_to = NULLIF($6, '')::uuid)
		   AND (NOT $7::boolean OR status <> 'done')
		   AND ($8 = '' OR deal_id = NULLIF($8, '')::uuid)
		   AND ($8 = '' OR lead_id = NULLIF($8, '')::uuid)
		 ORDER BY due_at ASC`,
		orgID, f.AccountID, f.Status, f.DueBefore, f.DueAfter, f.AssignedTo, f.ExcludeDone, f.LeadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Action, 0, 16)
	for rows.Next() {
		a, err := scanAction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *store) get(ctx context.Context, orgID, id string) (Action, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+actionColumns+` FROM follow_ups WHERE org_id = $1 AND id = $2`, orgID, id)
	return scanAction(row)
}

func (s *store) create(ctx context.Context, orgID string, in Input) (Action, error) {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO follow_ups (org_id, title, due_at, assigned_to, account_id, lead_id, deal_id, priority)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING `+actionColumns,
		orgID, in.Title, in.DueAt, in.AssignedTo, in.AccountID, in.LeadID, in.DealID, in.Priority)
	return scanAction(row)
}

func (s *store) update(ctx context.Context, orgID, id string, in Input) (Action, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE follow_ups
		 SET title = $3, due_at = $4, assigned_to = $5, account_id = $6, lead_id = $7, status = $8,
		     deal_id = $9, priority = $10,
		     completed_at = CASE WHEN $8 = 'done' THEN coalesce(completed_at, now()) ELSE NULL END,
		     updated_at = now()
		 WHERE org_id = $1 AND id = $2
		 RETURNING `+actionColumns,
		orgID, id, in.Title, in.DueAt, in.AssignedTo, in.AccountID, in.LeadID, in.Status, in.DealID,
		in.Priority)
	return scanAction(row)
}

func (s *store) delete(ctx context.Context, orgID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM follow_ups WHERE org_id = $1 AND id = $2`, orgID, id)
	if err != nil {
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

func (s *store) complete(ctx context.Context, orgID, id string) (Action, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE follow_ups
		 SET status = 'done', completed_at = now(), updated_at = now()
		 WHERE org_id = $1 AND id = $2
		 RETURNING `+actionColumns,
		orgID, id)
	return scanAction(row)
}

// accountExists reports whether accountID names a live account.
//
// accounts carries no org_id — no module in this codebase scopes clients or
// leads by organisation, only the rows this module owns — so existence plus the
// soft-delete flag is the same check the accounts module itself makes.
func (s *store) accountExists(ctx context.Context, accountID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM accounts WHERE id = $1 AND deleted_at IS NULL)`,
		accountID).Scan(&exists)
	if err != nil {
		if database.IsInvalidTextRepr(err) {
			return false, nil // malformed id can't name an account
		}
		return false, err
	}
	return exists, nil
}

// dealExists reports whether dealID names a live deal. Like accounts and leads,
// deals carry no org_id in this schema.
func (s *store) dealExists(ctx context.Context, dealID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM deals WHERE id = $1 AND deleted_at IS NULL)`,
		dealID).Scan(&exists)
	if err != nil {
		if database.IsInvalidTextRepr(err) {
			return false, nil // malformed id can't name a deal
		}
		return false, err
	}
	return exists, nil
}

// leadAccount looks up a live lead and reports the account it belongs to, so
// the caller can check that an action doesn't pair the two inconsistently.
// found is false when the lead is missing or deleted; account is nil when the
// lead exists but isn't attached to a client yet. Like accounts, leads carry no
// org_id to scope by.
func (s *store) leadAccount(ctx context.Context, leadID string) (account *string, found bool, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT account_id::text FROM leads WHERE id = $1 AND deleted_at IS NULL`,
		leadID).Scan(&account)
	if errors.Is(err, pgx.ErrNoRows) || database.IsInvalidTextRepr(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return account, true, nil
}

// assigneeInOrg reports whether userID names a user with a profile in orgID.
// Assignees must be members of the caller's organization and have a profile row
// to satisfy follow_ups_assigned_to_fkey.
func (s *store) assigneeInOrg(ctx context.Context, orgID, userID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM users u
			JOIN profiles p ON p.id = u.id
			WHERE u.org_id = $1 AND u.id = $2
		)`,
		orgID, userID).Scan(&exists)
	if err != nil {
		if database.IsInvalidTextRepr(err) {
			return false, nil
		}
		return false, err
	}
	return exists, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAction(row rowScanner) (Action, error) {
	var a Action
	err := row.Scan(&a.ID, &a.Title, &a.DueAt, &a.Status, &a.Priority, &a.AssignedTo, &a.AccountID,
		&a.LeadID, &a.DealID, &a.CompletedAt, &a.CreatedAt, &a.UpdatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), database.IsInvalidTextRepr(err):
		return Action{}, ErrNotFound
	case err != nil:
		return Action{}, err
	}
	return a, nil
}
