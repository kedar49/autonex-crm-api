package deals

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Stages is the deal lifecycle, in board order.
//
//	lead → qualified → proposal → won
//	                            ↘ lost
//
// Deliberately NOT the same list as leads.Stages: a deal has no "contacted"
// step — by the time it's a deal you've spoken to them. Matches dealSchema in
// shared/schemas and the CHECK constraint in migration 000005.
var Stages = []string{"lead", "qualified", "proposal", "won", "lost"}

// maxBoard caps a board fetch — well above a realistic pipeline, low enough that
// one org can't pull the whole table into memory.
const maxBoard = 500

// ValidationError is a rejected input, reported to the client as a 400.
type ValidationError struct{ msg string }

func (e ValidationError) Error() string { return e.msg }

func invalid(format string, args ...any) error {
	return ValidationError{msg: fmt.Sprintf(format, args...)}
}

// IsValidation reports whether err is a client input error (→ 400).
func IsValidation(err error) bool {
	var ve ValidationError
	return errors.As(err, &ve)
}

// Input is the writable shape of a deal.
type Input struct {
	Title             string     `json:"title"`
	Description       *string    `json:"description"`
	Amount            float64    `json:"amount"`
	Stage             string     `json:"stage"`
	OwnerUserID       *string    `json:"ownerUserId"`
	ContactID         *string    `json:"contactId"`
	AccountID         *string    `json:"accountId"`
	ExpectedCloseDate *time.Time `json:"expectedCloseDate"`
}

// Move is a drag-and-drop result: which column, and where in it.
type Move struct {
	Stage string `json:"stage"`
	Index int    `json:"index"`
}

// Board is the whole pipeline plus the stage order the client should render.
type Board struct {
	Stages []string `json:"stages"`
	Deals  []Deal   `json:"deals"`
}

// Service holds the deals business logic.
type Service struct {
	store *store
}

// NewService exposes the service to sibling modules (the dashboard aggregates
// deal stats). Handlers get theirs via NewHandler.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: &store{pool: pool}}
}

// Board returns every deal in the org, ordered by stage then manual position.
func (s *Service) Board(ctx context.Context, orgID string) (Board, error) {
	items, err := s.store.board(ctx, orgID, maxBoard)
	if err != nil {
		return Board{}, err
	}
	return Board{Stages: Stages, Deals: items}, nil
}

// Get returns a single deal from the caller's org.
func (s *Service) Get(ctx context.Context, orgID, id string) (Deal, error) {
	return s.store.get(ctx, orgID, id)
}

// Create validates and stores a new deal at the end of its column.
func (s *Service) Create(ctx context.Context, orgID string, in Input) (Deal, error) {
	in, err := s.prepare(ctx, orgID, in)
	if err != nil {
		return Deal{}, err
	}
	return s.store.create(ctx, orgID, in)
}

// Update replaces a deal's fields (a full PUT, not a merge).
func (s *Service) Update(ctx context.Context, orgID, id string, in Input) (Deal, error) {
	in, err := s.prepare(ctx, orgID, in)
	if err != nil {
		return Deal{}, err
	}
	return s.store.update(ctx, orgID, id, in)
}

// Delete removes a deal from the caller's org.
func (s *Service) Delete(ctx context.Context, orgID, id string) error {
	return s.store.delete(ctx, orgID, id)
}

// Move applies a drag-and-drop: change of column and/or position within one.
func (s *Service) Move(ctx context.Context, orgID, id string, mv Move) (Deal, error) {
	if !ValidStage(mv.Stage) {
		return Deal{}, invalid("unknown stage %q", mv.Stage)
	}
	return s.store.move(ctx, orgID, id, mv.Stage, mv.Index)
}

// Stats returns per-stage counts and amounts for the dashboard.
func (s *Service) Stats(ctx context.Context, orgID string) ([]Stats, error) {
	return s.store.stats(ctx, orgID)
}

// ValidStage reports whether stage is part of the lifecycle.
func ValidStage(stage string) bool {
	for _, s := range Stages {
		if s == stage {
			return true
		}
	}
	return false
}

func (s *Service) prepare(ctx context.Context, orgID string, in Input) (Input, error) {
	in = normalize(in)
	if err := validate(in); err != nil {
		return Input{}, err
	}

	// Every client-supplied foreign key needs an org check — the FK constraint
	// alone would accept another tenant's row.
	for _, ref := range []struct {
		table string
		id    *string
	}{
		{"users", in.OwnerUserID},
		{"contacts", in.ContactID},
		{"accounts", in.AccountID},
	} {
		if ref.id == nil {
			continue
		}
		ok, err := s.store.refInOrg(ctx, ref.table, orgID, *ref.id)
		if err != nil {
			return Input{}, err
		}
		if !ok {
			return Input{}, ErrRefNotFound
		}
	}
	return in, nil
}

// normalize trims text, blanks become NULL, and an omitted stage defaults to the
// start of the pipeline.
func normalize(in Input) Input {
	in.Title = strings.TrimSpace(in.Title)
	in.Description = trimmedOrNil(in.Description)
	in.OwnerUserID = trimmedOrNil(in.OwnerUserID)
	in.ContactID = trimmedOrNil(in.ContactID)
	in.AccountID = trimmedOrNil(in.AccountID)

	if strings.TrimSpace(in.Stage) == "" {
		in.Stage = Stages[0]
	}
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

// validate keeps a deal cheap to create: a title and a stage is enough. Unlike a
// lead, the amount is always present — it defaults to 0 rather than being
// nullable, because a deal without a number is just a lead.
func validate(in Input) error {
	switch {
	case in.Title == "":
		return invalid("title is required")
	case len(in.Title) > 160:
		return invalid("title must be 160 characters or fewer")
	case !ValidStage(in.Stage):
		return invalid("unknown stage %q", in.Stage)
	case in.Amount < 0:
		return invalid("amount cannot be negative")
	case in.Amount > 1e12:
		return invalid("amount must be 1,000,000,000,000 or less")
	}
	if in.Description != nil && len(*in.Description) > 5000 {
		return invalid("description must be 5000 characters or fewer")
	}
	return nil
}
