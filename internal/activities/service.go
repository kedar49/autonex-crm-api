package activities

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Kinds a person can log. "system" is deliberately absent: only the app writes
// those, through Log.
var Kinds = []string{"note", "call", "email", "meeting", "site_visit"}

const (
	defaultLimit = 50
	maxLimit     = 200
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

// Input is the writable shape of an activity.
type Input struct {
	Kind            string     `json:"kind"`
	Subject         *string    `json:"subject"`
	Body            *string    `json:"body"`
	OccurredAt      *time.Time `json:"occurredAt"`
	DurationMinutes *int       `json:"durationMinutes"`

	LeadID    *string `json:"leadId"`
	DealID    *string `json:"dealId"`
	AccountID *string `json:"accountId"`
	ContactID *string `json:"contactId"`
	QuoteID   *string `json:"quoteId"`
	InvoiceID *string `json:"invoiceId"`
}

// Service holds the activity-log business logic.
type Service struct {
	store *store
}

// NewService exposes the service to sibling modules.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: &store{pool: pool}}
}

// List returns a timeline, newest first.
func (s *Service) List(ctx context.Context, orgID string, f Filter) ([]Activity, error) {
	if f.Limit <= 0 {
		f.Limit = defaultLimit
	}
	if f.Limit > maxLimit {
		f.Limit = maxLimit
	}
	return s.store.list(ctx, orgID, f)
}

// Create records a human-logged activity.
func (s *Service) Create(ctx context.Context, orgID, userID string, in Input) (Activity, error) {
	in, err := s.prepare(ctx, orgID, in)
	if err != nil {
		return Activity{}, err
	}
	id, err := s.store.create(ctx, orgID, userID, in)
	if err != nil {
		return Activity{}, err
	}
	return s.store.get(ctx, orgID, id)
}

// Update edits a human-logged activity.
//
// What an activity is attached to is fixed at creation — the update statement
// doesn't touch those columns — so an edit is validated against the editable
// fields only. Running the create-time rules here would reject every edit for
// "not attached to anything", since a PUT body has no reason to repeat ids it
// cannot change.
func (s *Service) Update(ctx context.Context, orgID, id string, in Input) (Activity, error) {
	in = normalize(in)
	if err := validateEditable(in); err != nil {
		return Activity{}, err
	}
	if err := s.store.update(ctx, orgID, id, in); err != nil {
		return Activity{}, err
	}
	return s.store.get(ctx, orgID, id)
}

// Delete removes a human-logged activity.
func (s *Service) Delete(ctx context.Context, orgID, id string) error {
	return s.store.delete(ctx, orgID, id)
}

// ValidKind reports whether a person may log this kind.
func ValidKind(kind string) bool {
	for _, k := range Kinds {
		if k == kind {
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

	// Every id a client can supply is checked against the caller's org — the FK
	// constraints alone would accept another tenant's row.
	for _, ref := range []struct {
		table string
		id    *string
	}{
		{"leads", in.LeadID},
		{"deals", in.DealID},
		// An account is a company in this schema.
		{"companies", in.AccountID},
		{"contacts", in.ContactID},
		{"quotes", in.QuoteID},
		{"invoices", in.InvoiceID},
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
	in.Kind = strings.TrimSpace(in.Kind)
	if in.Kind == "" {
		in.Kind = "note"
	}
	in.Subject = trimmedOrNil(in.Subject)
	in.Body = trimmedOrNil(in.Body)
	in.LeadID = trimmedOrNil(in.LeadID)
	in.DealID = trimmedOrNil(in.DealID)
	in.AccountID = trimmedOrNil(in.AccountID)
	in.ContactID = trimmedOrNil(in.ContactID)
	in.QuoteID = trimmedOrNil(in.QuoteID)
	in.InvoiceID = trimmedOrNil(in.InvoiceID)
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

// validateEditable checks the fields an edit may change. Shared by create.
func validateEditable(in Input) error {
	if !ValidKind(in.Kind) {
		// "system" lands here too, which is the point: it is not something a
		// client may write.
		return invalid("unknown activity kind %q", in.Kind)
	}
	if in.Subject == nil && in.Body == nil {
		return invalid("an activity needs a subject or a note")
	}
	if in.Subject != nil && len(*in.Subject) > 200 {
		return invalid("subject must be 200 characters or fewer")
	}
	if in.Body != nil && len(*in.Body) > 5000 {
		return invalid("note must be 5000 characters or fewer")
	}
	if in.DurationMinutes != nil && (*in.DurationMinutes < 0 || *in.DurationMinutes > 24*60) {
		return invalid("duration must be between 0 and 1440 minutes")
	}
	return nil
}

// validate is the create-time rule set: the editable fields plus an attachment.
func validate(in Input) error {
	if err := validateEditable(in); err != nil {
		return err
	}

	// An activity attached to nothing would never appear on any timeline — it
	// would be silently lost rather than rejected.
	if in.LeadID == nil && in.DealID == nil && in.AccountID == nil &&
		in.ContactID == nil && in.QuoteID == nil && in.InvoiceID == nil {
		return invalid("an activity must be attached to a lead, deal, company, contact, quote or invoice")
	}
	return nil
}
