package leads

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Stages is the lead lifecycle, in board order. This is the source of truth:
// the DB CHECK constraint, the API and the kanban columns all follow it.
//
//	new → contacted → qualified → proposal → won
//	                                       ↘ lost
//
// "won" and "lost" are terminal; a lead can still be dragged back out of them,
// because in practice deals get revived.
var Stages = []string{"new", "contacted", "qualified", "proposal", "won", "lost"}

// maxBoard caps a board fetch. Well above a realistic pipeline, low enough that
// a runaway org can't pull the whole table into memory.
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

// Input is the writable shape of a lead.
type Input struct {
	FirstName   string   `json:"firstName"`
	LastName    *string  `json:"lastName"`
	Email       *string  `json:"email"`
	Phone       *string  `json:"phone"`
	Company     *string  `json:"company"`
	Source      *string  `json:"source"`
	Notes       *string  `json:"notes"`
	Value       *float64 `json:"value"`
	Stage       string   `json:"stage"`
	OwnerUserID *string  `json:"ownerUserId"`
}

// Move is a drag-and-drop result: which column, and where in it.
type Move struct {
	Stage string `json:"stage"`
	Index int    `json:"index"`
}

// Board is the whole pipeline plus the stage order the client should render.
type Board struct {
	Stages []string `json:"stages"`
	Leads  []Lead   `json:"leads"`
}

// Service holds the leads business logic.
type Service struct {
	store *store
}

// NewService exposes the service to sibling modules (the dashboard aggregates
// lead stats). Handlers get theirs via NewHandler.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: &store{pool: pool}}
}

// Board returns every lead in the org, ordered by stage then manual position.
func (s *Service) Board(ctx context.Context, orgID string) (Board, error) {
	leads, err := s.store.board(ctx, orgID, maxBoard)
	if err != nil {
		return Board{}, err
	}
	return Board{Stages: Stages, Leads: leads}, nil
}

// Get returns a single lead from the caller's org.
func (s *Service) Get(ctx context.Context, orgID, id string) (Lead, error) {
	return s.store.get(ctx, orgID, id)
}

// Create validates and stores a new lead at the end of its column.
func (s *Service) Create(ctx context.Context, orgID string, in Input) (Lead, error) {
	in, err := s.prepare(ctx, orgID, in)
	if err != nil {
		return Lead{}, err
	}
	return s.store.create(ctx, orgID, in)
}

// Update replaces a lead's fields (a full PUT, not a merge).
func (s *Service) Update(ctx context.Context, orgID, id string, in Input) (Lead, error) {
	in, err := s.prepare(ctx, orgID, in)
	if err != nil {
		return Lead{}, err
	}
	return s.store.update(ctx, orgID, id, in)
}

// Delete removes a lead from the caller's org.
func (s *Service) Delete(ctx context.Context, orgID, id string) error {
	return s.store.delete(ctx, orgID, id)
}

// Move applies a drag-and-drop: change of column and/or position within one.
func (s *Service) Move(ctx context.Context, orgID, id string, mv Move) (Lead, error) {
	if !ValidStage(mv.Stage) {
		return Lead{}, invalid("unknown stage %q", mv.Stage)
	}
	return s.store.move(ctx, orgID, id, mv.Stage, mv.Index)
}

// Stats returns per-stage counts and values for the dashboard.
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
	if in.OwnerUserID != nil {
		ok, err := s.store.ownerInOrg(ctx, orgID, *in.OwnerUserID)
		if err != nil {
			return Input{}, err
		}
		if !ok {
			return Input{}, ErrOwnerNotFound
		}
	}
	return in, nil
}

// normalize trims text, lower-cases the email, blanks become NULL, and an
// omitted stage defaults to the start of the pipeline.
func normalize(in Input) Input {
	in.FirstName = strings.TrimSpace(in.FirstName)
	in.LastName = trimmedOrNil(in.LastName)
	in.Phone = trimmedOrNil(in.Phone)
	in.Company = trimmedOrNil(in.Company)
	in.Source = trimmedOrNil(in.Source)
	in.Notes = trimmedOrNil(in.Notes)
	in.OwnerUserID = trimmedOrNil(in.OwnerUserID)

	if e := trimmedOrNil(in.Email); e != nil {
		lower := strings.ToLower(*e)
		in.Email = &lower
	} else {
		in.Email = nil
	}

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

// validate keeps a lead deliberately cheap to create: a name and a stage is
// enough. Everything else is filled in as the lead progresses, so requiring an
// email up front would just push people to type junk.
func validate(in Input) error {
	switch {
	case in.FirstName == "":
		return invalid("name is required")
	case len(in.FirstName) > 100:
		return invalid("name must be 100 characters or fewer")
	case !ValidStage(in.Stage):
		return invalid("unknown stage %q", in.Stage)
	}
	if in.Email != nil && (!strings.Contains(*in.Email, "@") || len(*in.Email) > 255) {
		return invalid("a valid email is required")
	}
	if in.Value != nil && (*in.Value < 0 || *in.Value > 1e12) {
		return invalid("value must be between 0 and 1,000,000,000,000")
	}
	if in.Notes != nil && len(*in.Notes) > 5000 {
		return invalid("notes must be 5000 characters or fewer")
	}
	return nil
}
