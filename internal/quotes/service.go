package quotes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Statuses is the quote lifecycle, in display order. These are the values the
// deployed database enforces through quotes_status_check — "approved" and
// "rejected" rather than the "accepted" and "declined" the original model used.
var Statuses = []string{"draft", "sent", "approved", "rejected", "expired"}

// transitions is the allowed state machine.
//
//	draft → sent → approved   (terminal: an approved quote is what an invoice
//	              ↘ rejected   will be raised from)
//	              ↘ expired
//
// rejected and expired can be revised back to draft, which is what actually
// happens when a customer comes back. Approved deliberately has no way out: it
// is the record that a price was agreed.
var transitions = map[string][]string{
	"draft":    {"sent"},
	"sent":     {"approved", "rejected", "expired", "draft"},
	"approved": {},
	"rejected": {"draft"},
	"expired":  {"draft"},
}

// Pagination bounds, matching the other list endpoints.
const (
	defaultLimit = 25
	maxLimit     = 100
	maxItems     = 200
)

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

// ItemInput is one writable line. Note there is no line total: it is derived.
type ItemInput struct {
	Description     string  `json:"description"`
	Quantity        float64 `json:"quantity"`
	UnitPrice       float64 `json:"unitPrice"`
	DiscountPercent float64 `json:"discountPercent"`
	TaxPercent      float64 `json:"taxPercent"`
}

// Input is the writable shape of a quote. Totals, number, status and currency are
// all server-owned and absent here by design.
type Input struct {
	Title       *string     `json:"title"`
	AccountID   *string     `json:"accountId"`
	ContactID   *string     `json:"contactId"`
	DealID      *string     `json:"dealId"`
	OwnerUserID *string     `json:"ownerUserId"`
	Notes       *string     `json:"notes"`
	ValidUntil  *time.Time  `json:"validUntil"`
	Items       []ItemInput `json:"items"`
}

// Page is one page of quotes plus the totals a list UI needs.
type Page struct {
	Items  []Quote `json:"items"`
	Total  int     `json:"total"`
	Limit  int     `json:"limit"`
	Offset int     `json:"offset"`
}

// Service holds the quotes business logic.
type Service struct {
	store *store
	pool  *pgxpool.Pool
}

// NewService exposes the service to sibling modules (the dashboard reads stats).
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: &store{pool: pool}, pool: pool}
}

// List returns one org-scoped page, optionally filtered by status.
func (s *Service) List(ctx context.Context, orgID, status string, limit, offset int) (Page, error) {
	if status != "" && !ValidStatus(status) {
		return Page{}, invalid("unknown status %q", status)
	}
	limit, offset = clampPage(limit, offset)

	items, err := s.store.list(ctx, orgID, status, limit, offset)
	if err != nil {
		return Page{}, err
	}
	total, err := s.store.count(ctx, orgID, status)
	if err != nil {
		return Page{}, err
	}
	return Page{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

// Get returns a single quote, with its line items.
func (s *Service) Get(ctx context.Context, orgID, id string) (Quote, error) {
	return s.store.get(ctx, orgID, id)
}

// Create validates, allocates a number, and stores the quote and its items.
func (s *Service) Create(ctx context.Context, orgID string, in Input) (Quote, error) {
	in, err := s.prepare(ctx, orgID, in)
	if err != nil {
		return Quote{}, err
	}

	// The document records the currency it was issued in, so later changes to the
	// workspace setting can't silently reprice history.
	var currency string
	if err := s.pool.QueryRow(ctx,
		`SELECT currency FROM organizations WHERE id = $1`, orgID).Scan(&currency); err != nil {
		return Quote{}, err
	}

	id, err := s.store.create(ctx, orgID, currency, in)
	if err != nil {
		return Quote{}, err
	}
	return s.store.get(ctx, orgID, id)
}

// Update replaces a draft's header and line items.
func (s *Service) Update(ctx context.Context, orgID, id string, in Input) (Quote, error) {
	in, err := s.prepare(ctx, orgID, in)
	if err != nil {
		return Quote{}, err
	}
	if err := s.store.update(ctx, orgID, id, in); err != nil {
		return Quote{}, err
	}
	return s.store.get(ctx, orgID, id)
}

// SetStatus applies a lifecycle transition.
func (s *Service) SetStatus(ctx context.Context, orgID, id, to string) (Quote, error) {
	if !ValidStatus(to) {
		return Quote{}, invalid("unknown status %q", to)
	}

	from, err := s.store.currentStatus(ctx, orgID, id)
	if err != nil {
		return Quote{}, err
	}
	if from == to {
		return s.store.get(ctx, orgID, id)
	}
	if !CanTransition(from, to) {
		// Phrased to avoid an a/an choice — "a accepted quote" reads as a bug.
		return Quote{}, invalid("a quote that is %s cannot become %s", from, to)
	}

	if err := s.store.setStatus(ctx, orgID, id, from, to); err != nil {
		return Quote{}, err
	}
	return s.store.get(ctx, orgID, id)
}

// Delete removes a draft quote.
func (s *Service) Delete(ctx context.Context, orgID, id string) error {
	return s.store.delete(ctx, orgID, id)
}

// Stats returns per-status counts and value for the dashboard.
func (s *Service) Stats(ctx context.Context, orgID string) ([]Stats, error) {
	return s.store.stats(ctx, orgID)
}

// ValidStatus reports whether status is part of the lifecycle.
func ValidStatus(status string) bool {
	for _, s := range Statuses {
		if s == status {
			return true
		}
	}
	return false
}

// CanTransition reports whether from → to is an allowed move.
func CanTransition(from, to string) bool {
	for _, allowed := range transitions[from] {
		if allowed == to {
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

	// Every client-supplied foreign key needs an org check — the FK constraints
	// alone would accept another tenant's row.
	for _, ref := range []struct {
		table string
		id    *string
	}{
		{"companies", in.AccountID},
		{"contacts", in.ContactID},
		{"deals", in.DealID},
		{"users", in.OwnerUserID},
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

func normalize(in Input) Input {
	in.Title = trimmedOrNil(in.Title)
	in.Notes = trimmedOrNil(in.Notes)
	in.AccountID = trimmedOrNil(in.AccountID)
	in.ContactID = trimmedOrNil(in.ContactID)
	in.DealID = trimmedOrNil(in.DealID)
	in.OwnerUserID = trimmedOrNil(in.OwnerUserID)

	// Drop blank lines rather than rejecting the document: an editor that always
	// shows an empty row would otherwise be unsubmittable.
	kept := make([]ItemInput, 0, len(in.Items))
	for _, item := range in.Items {
		item.Description = strings.TrimSpace(item.Description)
		if item.Description == "" && item.Quantity == 0 && item.UnitPrice == 0 {
			continue
		}
		kept = append(kept, item)
	}
	in.Items = kept
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

func validate(in Input) error {
	if in.Title != nil && len(*in.Title) > 160 {
		return invalid("title must be 160 characters or fewer")
	}
	if in.Notes != nil && len(*in.Notes) > 5000 {
		return invalid("notes must be 5000 characters or fewer")
	}
	if len(in.Items) == 0 {
		return invalid("a quote needs at least one line item")
	}
	if len(in.Items) > maxItems {
		return invalid("a quote can have at most %d line items", maxItems)
	}

	for i, item := range in.Items {
		line := i + 1
		switch {
		case item.Description == "":
			return invalid("line %d needs a description", line)
		case len(item.Description) > 500:
			return invalid("line %d: description must be 500 characters or fewer", line)
		case item.Quantity < 0 || item.Quantity > 1e6:
			return invalid("line %d: quantity must be between 0 and 1,000,000", line)
		case item.UnitPrice < 0 || item.UnitPrice > 1e10:
			return invalid("line %d: unit price is out of range", line)
		case item.DiscountPercent < 0 || item.DiscountPercent > 100:
			return invalid("line %d: discount must be between 0 and 100%%", line)
		case item.TaxPercent < 0 || item.TaxPercent > 100:
			return invalid("line %d: tax must be between 0 and 100%%", line)
		}
	}
	return nil
}

// clampPage keeps a client from asking for an unbounded or nonsensical page.
func clampPage(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// deref turns an optional id into the empty string activities.Entry expects.
func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
