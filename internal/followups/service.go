package followups

import (
	"context"
	"strings"
	"time"

	"github.com/go-crm/services/pkg/apperr"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Statuses is the canonical order an action's status can be in.
var Statuses = []string{"open", "in_progress", "done"}

func validStatus(s string) bool {
	for _, v := range Statuses {
		if v == s {
			return true
		}
	}
	return false
}

// Input is the writable shape of an action (create and update share it).
// Status is only meaningful on update — a new action always starts "open".
type Input struct {
	Title      string    `json:"title"`
	DueAt      time.Time `json:"dueAt"`
	AssignedTo *string   `json:"assignedTo"`
	AccountID  *string   `json:"accountId"`
	LeadID     *string   `json:"leadId"`
	Status     string    `json:"status"`
}

// Service holds the Actions business logic.
type Service struct {
	store *store
}

func newService(pool *pgxpool.Pool) *Service {
	return &Service{store: &store{pool: pool}}
}

func (s *Service) List(ctx context.Context, orgID string, f Filter) ([]Action, error) {
	return s.store.list(ctx, orgID, f)
}

func (s *Service) Get(ctx context.Context, orgID, id string) (Action, error) {
	return s.store.get(ctx, orgID, id)
}

// Create validates and stores a new action. It always starts "open" —
// Input.Status is ignored here, and only consulted by Update.
func (s *Service) Create(ctx context.Context, orgID string, in Input) (Action, error) {
	in, err := s.prepare(ctx, orgID, in, false)
	if err != nil {
		return Action{}, err
	}
	return s.store.create(ctx, orgID, in)
}

// Update replaces an action's fields, including its status.
func (s *Service) Update(ctx context.Context, orgID, id string, in Input) (Action, error) {
	in, err := s.prepare(ctx, orgID, in, true)
	if err != nil {
		return Action{}, err
	}
	return s.store.update(ctx, orgID, id, in)
}

func (s *Service) Delete(ctx context.Context, orgID, id string) error {
	return s.store.delete(ctx, orgID, id)
}

// Complete is a shortcut for Update that only ever moves status to "done",
// without requiring the caller to resend title/due date/assignee.
func (s *Service) Complete(ctx context.Context, orgID, id string) (Action, error) {
	return s.store.complete(ctx, orgID, id)
}

// prepare normalizes and validates input, and confirms any referenced account
// or assignee belongs to the caller's org. requireStatus is true on update,
// where the client picks the status; create always starts "open".
func (s *Service) prepare(ctx context.Context, orgID string, in Input, requireStatus bool) (Input, error) {
	in = normalize(in)
	if err := validate(in, requireStatus); err != nil {
		return Input{}, err
	}
	if !requireStatus {
		in.Status = "open"
	}

	if in.AccountID != nil {
		ok, err := s.store.accountExists(ctx, *in.AccountID)
		if err != nil {
			return Input{}, err
		}
		if !ok {
			return Input{}, ErrAccountNotFound
		}
	}
	if in.LeadID != nil {
		leadAccount, ok, err := s.store.leadAccount(ctx, *in.LeadID)
		if err != nil {
			return Input{}, err
		}
		if !ok {
			return Input{}, ErrLeadNotFound
		}
		// A lead sits with at most one client, so an action naming both must
		// name the same one. The dialog clears the lead when the account
		// changes; this is the same rule for callers that skip the UI.
		if in.AccountID != nil && leadAccount != nil && *leadAccount != *in.AccountID {
			return Input{}, ErrLeadAccountMismatch
		}
	}
	if in.AssignedTo != nil {
		ok, err := s.store.assigneeInOrg(ctx, orgID, *in.AssignedTo)
		if err != nil {
			return Input{}, err
		}
		if !ok {
			return Input{}, ErrAssigneeNotFound
		}
	}
	return in, nil
}

func normalize(in Input) Input {
	in.Title = strings.TrimSpace(in.Title)
	in.AssignedTo = trimmedOrNil(in.AssignedTo)
	in.AccountID = trimmedOrNil(in.AccountID)
	in.LeadID = trimmedOrNil(in.LeadID)
	in.Status = strings.TrimSpace(in.Status)
	return in
}

func trimmedOrNil(v *string) *string {
	if v == nil {
		return nil
	}
	t := strings.TrimSpace(*v)
	if t == "" {
		return nil
	}
	return &t
}

func validate(in Input, requireStatus bool) error {
	if in.Title == "" {
		return apperr.Invalid("title is required")
	}
	if len(in.Title) > 200 {
		return apperr.Invalid("title must be 200 characters or fewer")
	}
	if in.DueAt.IsZero() {
		return apperr.Invalid("dueAt is required")
	}
	if requireStatus && !validStatus(in.Status) {
		return apperr.Invalid("status must be one of open, in_progress, done")
	}
	return nil
}
