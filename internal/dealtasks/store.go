// Package dealtasks is the checklist that hangs off a deal card.
//
// Tasks are the light half of the deal card's two panels: no due date, no
// dashboard, just "what needs doing on this deal", ticked off in place. The
// heavier half is the Actions module, which owns work that has an owner, a due
// date and a board reporting on it.
//
// What tasks do record is who did what: created_by and completed_by are real
// foreign keys, because "who closed this off" is only worth asking if the
// answer cannot be quietly rewritten.
package dealtasks

import (
	"context"
	"errors"
	"time"

	"github.com/go-crm/services/pkg/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound means no task with that id is visible to the caller.
	ErrNotFound = errors.New("task not found")
	// ErrDealNotFound means the referenced deal is missing or deleted.
	ErrDealNotFound = errors.New("deal not found")
	// ErrAssigneeNotFound means the assignee is not a member of the caller's org.
	ErrAssigneeNotFound = errors.New("assignee not found")
)

// Task is one checklist row on a deal card. The three people are denormalized
// to their display names by the store's joins, so a card renders without
// looking anyone up.
type Task struct {
	ID       string `json:"id"`
	DealID   string `json:"dealId"`
	Text     string `json:"text"`
	Priority string `json:"priority"`
	Position int    `json:"position"`

	AssignedTo      *string `json:"assignedTo"`
	AssignedToName  *string `json:"assignedToName"`
	CreatedBy       *string `json:"createdBy"`
	CreatedByName   *string `json:"createdByName"`
	CompletedBy     *string `json:"completedBy"`
	CompletedByName *string `json:"completedByName"`

	Done        bool       `json:"done"`
	CompletedAt *time.Time `json:"completedAt"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type store struct {
	pool *pgxpool.Pool
}

const taskColumns = `
	t.id::text, t.deal_id::text, t.text, t.priority, t.position,
	t.assigned_to::text, pa.full_name,
	t.created_by::text, pc.full_name,
	t.completed_by::text, pd.full_name,
	t.done, t.completed_at, t.created_at, t.updated_at`

const taskFrom = `
	FROM deal_tasks t
	LEFT JOIN profiles pa ON pa.id = t.assigned_to
	LEFT JOIN profiles pc ON pc.id = t.created_by
	LEFT JOIN profiles pd ON pd.id = t.completed_by `

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(row rowScanner) (Task, error) {
	var t Task
	err := row.Scan(
		&t.ID, &t.DealID, &t.Text, &t.Priority, &t.Position,
		&t.AssignedTo, &t.AssignedToName,
		&t.CreatedBy, &t.CreatedByName,
		&t.CompletedBy, &t.CompletedByName,
		&t.Done, &t.CompletedAt, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

// list returns a deal's tasks, or every live deal's tasks when dealID is empty
// — the board loads the whole set in one request and groups them per card.
func (s *store) list(ctx context.Context, dealID string) ([]Task, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+taskColumns+taskFrom+`
		 JOIN deals d ON d.id = t.deal_id AND d.deleted_at IS NULL
		 WHERE ($1 = '' OR t.deal_id = NULLIF($1, '')::uuid)
		 ORDER BY t.done, t.position, t.created_at`, dealID)
	if err != nil {
		if database.IsInvalidTextRepr(err) {
			return []Task{}, nil // a malformed id names no deal
		}
		return nil, err
	}
	defer rows.Close()

	out := make([]Task, 0, 16)
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *store) get(ctx context.Context, id string) (Task, error) {
	t, err := scanTask(s.pool.QueryRow(ctx,
		`SELECT `+taskColumns+taskFrom+` WHERE t.id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) || database.IsInvalidTextRepr(err) {
		return Task{}, ErrNotFound
	}
	return t, err
}

func (s *store) create(ctx context.Context, in Input, createdBy string) (Task, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`INSERT INTO deal_tasks (deal_id, text, priority, assigned_to, created_by, position)
		 VALUES ($1::uuid, $2, $3, $4::uuid, $5::uuid,
		         coalesce((SELECT max(position) + 1 FROM deal_tasks WHERE deal_id = $1::uuid), 0))
		 RETURNING id::text`,
		in.DealID, in.Text, in.Priority, in.AssignedTo, nilIfEmpty(createdBy)).Scan(&id)
	if err != nil {
		return Task{}, err
	}
	return s.get(ctx, id)
}

// update rewrites a task's editable fields. Completion is bookkeeping the
// caller does not get to set: ticking a task stamps who did it and when, and
// un-ticking clears both, so the audit trail always matches the state.
func (s *store) update(ctx context.Context, id string, in Input, actor string) (Task, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE deal_tasks SET
		     text = $2,
		     priority = $3,
		     assigned_to = $4::uuid,
		     done = $5,
		     completed_by = CASE WHEN $5 THEN coalesce(completed_by, $6::uuid) END,
		     completed_at = CASE WHEN $5 THEN coalesce(completed_at, now()) END
		 WHERE id = $1::uuid`,
		id, in.Text, in.Priority, in.AssignedTo, in.Done, nilIfEmpty(actor))
	if err != nil {
		if database.IsInvalidTextRepr(err) {
			return Task{}, ErrNotFound
		}
		return Task{}, err
	}
	if tag.RowsAffected() == 0 {
		return Task{}, ErrNotFound
	}
	return s.get(ctx, id)
}

func (s *store) delete(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM deal_tasks WHERE id = $1::uuid`, id)
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

func (s *store) dealExists(ctx context.Context, dealID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM deals WHERE id = $1::uuid AND deleted_at IS NULL)`,
		dealID).Scan(&exists)
	if err != nil {
		if database.IsInvalidTextRepr(err) {
			return false, nil
		}
		return false, err
	}
	return exists, nil
}

// assigneeInOrg mirrors the Actions module: an assignee must be a user of the
// caller's organisation with a profile row, which is what the FK points at.
func (s *store) assigneeInOrg(ctx context.Context, orgID, userID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM users u
			JOIN profiles p ON p.id = u.id
			WHERE u.org_id = $1::uuid AND u.id = $2::uuid
		)`, orgID, userID).Scan(&exists)
	if err != nil {
		if database.IsInvalidTextRepr(err) {
			return false, nil
		}
		return false, err
	}
	return exists, nil
}

func nilIfEmpty(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
