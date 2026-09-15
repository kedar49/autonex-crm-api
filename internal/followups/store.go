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

	"github.com/go-crm/services/pkg/database"
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
)

// Action is one row of the Actions dashboard.
type Action struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	DueAt       time.Time  `json:"dueAt"`
	Status      string     `json:"status"`
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

const actionColumns = `id::text, title, due_at, status, assigned_to::text, account_id::text,
	lead_id::text, deal_id::text, completed_at, created_at, updated_at`

// Filter narrows the org's action list; every field is optional (a zero value
// means "no opinion"), and an empty filter returns the org's whole list.
type Filter struct {
	AccountID string
	Status    string
	DueBefore *time.Time
	DueAfter  *time.Time
}

func (s *store) list(ctx context.Context, orgID string, f Filter) ([]Action, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+actionColumns+`
		 FROM follow_ups
		 WHERE org_id = $1
		   AND ($2 = '' OR account_id = $2::uuid)
		   AND ($3 = '' OR status = $3)
		   AND ($4::timestamptz IS NULL OR due_at <= $4)
		   AND ($5::timestamptz IS NULL OR due_at >= $5)
		 ORDER BY due_at ASC`,
		orgID, f.AccountID, f.Status, f.DueBefore, f.DueAfter)
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
		`INSERT INTO follow_ups (org_id, title, due_at, assigned_to, account_id)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING `+actionColumns,
		orgID, in.Title, in.DueAt, in.AssignedTo, in.AccountID)
	return scanAction(row)
}

func (s *store) update(ctx context.Context, orgID, id string, in Input) (Action, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE follow_ups
		 SET title = $3, due_at = $4, assigned_to = $5, account_id = $6, status = $7,
		     completed_at = CASE WHEN $7 = 'done' THEN coalesce(completed_at, now()) ELSE NULL END,
		     updated_at = now()
		 WHERE org_id = $1 AND id = $2
		 RETURNING `+actionColumns,
		orgID, id, in.Title, in.DueAt, in.AssignedTo, in.AccountID, in.Status)
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

// accountInOrg reports whether accountID names an account in orgID.
func (s *store) accountInOrg(ctx context.Context, orgID, accountID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM accounts WHERE org_id = $1 AND id = $2)`,
		orgID, accountID).Scan(&exists)
	if err != nil {
		if database.IsInvalidTextRepr(err) {
			return false, nil // malformed id can't name an account
		}
		return false, err
	}
	return exists, nil
}

// assigneeInOrg reports whether userID names a user in orgID. Assignees are
// users, not profiles — profiles carries no org_id of its own.
func (s *store) assigneeInOrg(ctx context.Context, orgID, userID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE org_id = $1 AND id = $2)`,
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
	err := row.Scan(&a.ID, &a.Title, &a.DueAt, &a.Status, &a.AssignedTo, &a.AccountID,
		&a.LeadID, &a.DealID, &a.CompletedAt, &a.CreatedAt, &a.UpdatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), database.IsInvalidTextRepr(err):
		return Action{}, ErrNotFound
	case err != nil:
		return Action{}, err
	}
	return a, nil
}
