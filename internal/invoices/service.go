package invoices

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Statuses is the invoice lifecycle, in display order.
var Statuses = []string{"draft", "sent", "paid", "void"}

// transitions is the allowed state machine.
//
//	draft → sent → paid
//	           ↘ void
//	  ↘ void
//
// There is no way back from paid or void, and no way to un-send: once a numbered
// demand has gone to a customer, the correction is a credit note or a void, not
// an edit. "Overdue" is not in here at all — it is derived from the due date.
var transitions = map[string][]string{
	"draft": {"sent", "void"},
	"sent":  {"paid", "void"},
	"paid":  {},
	"void":  {},
}

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

// ItemInput is one writable line; the line total is derived.
type ItemInput struct {
	Description     string  `json:"description"`
	Quantity        float64 `json:"quantity"`
	UnitPrice       float64 `json:"unitPrice"`
	DiscountPercent float64 `json:"discountPercent"`
	TaxPercent      float64 `json:"taxPercent"`
}

// Input is the writable shape. Number, status, currency, totals and amount paid
// are all server-owned and absent by design.
type Input struct {
	Title       *string     `json:"title"`
	AccountID   *string     `json:"accountId"`
	ContactID   *string     `json:"contactId"`
	DealID      *string     `json:"dealId"`
	OwnerUserID *string     `json:"ownerUserId"`
	Notes       *string     `json:"notes"`
	IssueDate   *time.Time  `json:"issueDate"`
	DueDate     *time.Time  `json:"dueDate"`
	Items       []ItemInput `json:"items"`
}

// PaymentInput records a receipt.
type PaymentInput struct {
	Amount    float64    `json:"amount"`
	PaidOn    *time.Time `json:"paidOn"`
	Method    *string    `json:"method"`
	Reference *string    `json:"reference"`
	Note      *string    `json:"note"`
}

// Page is one page of invoices plus the totals a list UI needs.
type Page struct {
	Items  []Invoice `json:"items"`
	Total  int       `json:"total"`
	Limit  int       `json:"limit"`
	Offset int       `json:"offset"`
}

// Service holds the invoices business logic.
type Service struct {
	store *store
	pool  *pgxpool.Pool
}

// NewService exposes the service to sibling modules (the dashboard reads stats).
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: &store{pool: pool}, pool: pool}
}

// List returns one org-scoped page, optionally filtered by status or "overdue".
func (s *Service) List(ctx context.Context, orgID, status string, limit, offset int) (Page, error) {
	if status != "" && status != "overdue" && !ValidStatus(status) {
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

// Get returns a single invoice with its items and payments.
func (s *Service) Get(ctx context.Context, orgID, id string) (Invoice, error) {
	return s.store.get(ctx, orgID, id)
}

// Create validates, numbers, and stores an invoice and its items.
func (s *Service) Create(ctx context.Context, orgID string, in Input) (Invoice, error) {
	in, err := s.prepare(ctx, orgID, in)
	if err != nil {
		return Invoice{}, err
	}

	currency, err := s.orgCurrency(ctx, orgID)
	if err != nil {
		return Invoice{}, err
	}

	id, err := s.store.create(ctx, orgID, currency, in)
	if err != nil {
		return Invoice{}, err
	}
	return s.store.get(ctx, orgID, id)
}

// Update replaces a draft's header and line items.
func (s *Service) Update(ctx context.Context, orgID, id string, in Input) (Invoice, error) {
	in, err := s.prepare(ctx, orgID, in)
	if err != nil {
		return Invoice{}, err
	}
	if err := s.store.update(ctx, orgID, id, in); err != nil {
		return Invoice{}, err
	}
	return s.store.get(ctx, orgID, id)
}

// SetStatus applies a lifecycle transition.
func (s *Service) SetStatus(ctx context.Context, orgID, id, to string) (Invoice, error) {
	if !ValidStatus(to) {
		return Invoice{}, invalid("unknown status %q", to)
	}

	from, err := s.store.currentStatus(ctx, orgID, id)
	if err != nil {
		return Invoice{}, err
	}
	if from == to {
		return s.store.get(ctx, orgID, id)
	}
	if !CanTransition(from, to) {
		return Invoice{}, invalid("an invoice that is %s cannot become %s", from, to)
	}

	if err := s.store.setStatus(ctx, orgID, id, from, to); err != nil {
		return Invoice{}, err
	}
	return s.store.get(ctx, orgID, id)
}

// RecordPayment appends a receipt and settles the invoice if it is now covered.
func (s *Service) RecordPayment(ctx context.Context, orgID, id string, in PaymentInput) (Invoice, error) {
	in.Method = trimmedOrNil(in.Method)
	in.Reference = trimmedOrNil(in.Reference)
	in.Note = trimmedOrNil(in.Note)

	switch {
	case in.Amount <= 0:
		return Invoice{}, invalid("a payment must be greater than zero")
	case in.Amount > 1e12:
		return Invoice{}, invalid("that payment amount is out of range")
	}

	if err := s.store.addPayment(ctx, orgID, id, in); err != nil {
		return Invoice{}, err
	}
	return s.store.get(ctx, orgID, id)
}

// Delete removes a draft invoice.
func (s *Service) Delete(ctx context.Context, orgID, id string) error {
	return s.store.delete(ctx, orgID, id)
}

// Stats returns billing totals for the dashboard.
func (s *Service) Stats(ctx context.Context, orgID string) (Stats, error) {
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

func (s *Service) orgCurrency(ctx context.Context, orgID string) (string, error) {
	var currency string
	err := s.pool.QueryRow(ctx,
		`SELECT currency FROM organizations WHERE id = $1`, orgID).Scan(&currency)
	return currency, err
}

func (s *Service) prepare(ctx context.Context, orgID string, in Input) (Input, error) {
	in = normalize(in)
	if err := validate(in); err != nil {
		return Input{}, err
	}

	for _, ref := range []struct {
		table string
		id    *string
	}{
		{"accounts", in.AccountID},
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
	if in.IssueDate != nil && in.DueDate != nil && in.DueDate.Before(*in.IssueDate) {
		return invalid("the due date cannot be before the issue date")
	}
	if len(in.Items) == 0 {
		return invalid("an invoice needs at least one line item")
	}
	if len(in.Items) > maxItems {
		return invalid("an invoice can have at most %d line items", maxItems)
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
