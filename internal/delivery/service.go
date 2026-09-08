// Package delivery is the client delivery tracker: the spreadsheet operations
// keeps of what has been sold, where it is going in, and what is left to do.
//
// It is deliberately not part of the deals pipeline. A tracker row describes an
// installation at a client, which is not one-to-one with a deal — one rollout
// can be fed by several deals, and rows get typed before anyone opens the board.
// Keeping them separate means the table behaves like the sheet it replaces.
package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound means no tracker row with that id exists in this organization.
	ErrNotFound = errors.New("tracker row not found")
	// ErrClientTaken means another row in this organization already tracks that
	// client. Import upserts on the client name, so it has to stay unique.
	ErrClientTaken = errors.New("a row for that client already exists")
)

// maxRows caps a list fetch. The tracker is a working sheet, not an archive; a
// tenant with more rows than this has outgrown the screen anyway.
const maxRows = 2000

// positionStep leaves room between rows so inserting one does not renumber the
// table.
const positionStep = 1000

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

// Row is one line of the tracker.
//
// Every field but the client is optional, and the free-text ones stay free text:
// these values arrive by pasting or importing a sheet, and a row someone has
// half-filled is worth more stored than rejected.
type Row struct {
	ID                 string  `json:"id"`
	Client             string  `json:"client"`
	Products           *string `json:"products"`
	Locations          *string `json:"locations"`
	TotalCameras       *int    `json:"totalCameras"`
	Status             *string `json:"status"`
	ImplementationDate *Date   `json:"implementationDate"`
	CurrentStages      *string `json:"currentStages"`
	KeyContacts        *string `json:"keyContacts"`
	NextSteps          *string `json:"nextSteps"`
	Notes              *string `json:"notes"`
	Position           int     `json:"position"`
	// The deal this row is delivering, denormalized so the grid can show the
	// pipeline stage without a lookup per row. Null for rows typed or imported
	// before any deal existed.
	DealID        *string   `json:"dealId"`
	DealTitle     *string   `json:"dealTitle"`
	DealStage     *string   `json:"dealStage"`
	UpdatedBy     *string   `json:"updatedBy"`
	UpdatedByName *string   `json:"updatedByName"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// Input is the writable shape of a row. Pointer fields distinguish "clear this
// cell" (explicit null) from the zero value.
type Input struct {
	Client             string  `json:"client"`
	Products           *string `json:"products"`
	Locations          *string `json:"locations"`
	TotalCameras       *int    `json:"totalCameras"`
	Status             *string `json:"status"`
	ImplementationDate *Date   `json:"implementationDate"`
	CurrentStages      *string `json:"currentStages"`
	KeyContacts        *string `json:"keyContacts"`
	NextSteps          *string `json:"nextSteps"`
	Notes              *string `json:"notes"`
}

// Page is a list response.
type Page struct {
	Items []Row `json:"items"`
	Total int   `json:"total"`
}

// Service holds the tracker's business logic.
type Service struct{ store *store }

func newService(pool *pgxpool.Pool) *Service {
	return &Service{store: &store{pool: pool}}
}

// List returns the tracker in the order the user arranged it.
func (s *Service) List(ctx context.Context, orgID string) (Page, error) {
	items, err := s.store.list(ctx, orgID, maxRows)
	if err != nil {
		return Page{}, err
	}
	return Page{Items: items, Total: len(items)}, nil
}

// Create adds a row at the bottom of the table.
func (s *Service) Create(ctx context.Context, orgID, userID string, in Input) (Row, error) {
	clean, err := validate(in)
	if err != nil {
		return Row{}, err
	}
	return s.store.create(ctx, orgID, userID, clean)
}

// Update rewrites a row. The whole row is sent even when one cell changed: the
// table edits in place, and a per-cell endpoint would multiply round trips
// without making a concurrent edit any safer.
func (s *Service) Update(ctx context.Context, orgID, userID, id string, in Input) (Row, error) {
	clean, err := validate(in)
	if err != nil {
		return Row{}, err
	}
	return s.store.update(ctx, orgID, userID, id, clean)
}

// Delete removes a row outright. The tracker has no soft-delete: a line deleted
// off a spreadsheet is gone, and pretending otherwise would need an undo UI that
// does not exist.
func (s *Service) Delete(ctx context.Context, orgID, id string) error {
	return s.store.delete(ctx, orgID, id)
}

// Reorder applies a new row order. ids is the full list, top to bottom; ids that
// do not belong to this organization are ignored rather than erroring, because a
// stale tab reordering a row someone else deleted is not a client mistake worth
// a red banner.
func (s *Service) Reorder(ctx context.Context, orgID string, ids []string) error {
	if len(ids) == 0 {
		return invalid("send the row order")
	}
	if len(ids) > maxRows {
		return invalid("too many rows in one reorder")
	}
	return s.store.reorder(ctx, orgID, ids)
}

// validate normalizes an input and rejects the one thing a row cannot do
// without: a client name to be filed under.
func validate(in Input) (Input, error) {
	in.Client = strings.TrimSpace(in.Client)
	if in.Client == "" {
		return Input{}, invalid("client is required")
	}
	if len([]rune(in.Client)) > 200 {
		return Input{}, invalid("client name is too long")
	}
	if in.TotalCameras != nil && *in.TotalCameras < 0 {
		return Input{}, invalid("total cameras cannot be negative")
	}

	in.Products = trimOptional(in.Products)
	in.Locations = trimOptional(in.Locations)
	in.Status = trimOptional(in.Status)
	in.CurrentStages = trimOptional(in.CurrentStages)
	in.KeyContacts = trimOptional(in.KeyContacts)
	in.NextSteps = trimOptional(in.NextSteps)
	in.Notes = trimOptional(in.Notes)
	return in, nil
}

// trimOptional trims a nullable string and collapses a now-empty one to NULL, so
// clearing a cell in the UI stores nothing rather than "".
func trimOptional(v *string) *string {
	if v == nil {
		return nil
	}
	t := strings.TrimSpace(*v)
	if t == "" {
		return nil
	}
	return &t
}
