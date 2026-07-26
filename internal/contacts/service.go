package contacts

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pagination bounds for list requests.
const (
	defaultLimit = 25
	maxLimit     = 100
)

// ValidationError is a rejected input, reported to the client as a 400 with its
// message. Anything else from the service is a 500.
type ValidationError struct{ msg string }

func (e ValidationError) Error() string { return e.msg }

func invalid(format string, args ...any) error {
	return ValidationError{msg: fmt.Sprintf(format, args...)}
}

// Input is the writable shape of a contact (create and update share it).
type Input struct {
	FirstName string  `json:"firstName"`
	LastName  string  `json:"lastName"`
	Email     string  `json:"email"`
	Phone     *string `json:"phone"`
	AccountID *string `json:"accountId"`
}

// Page is one page of contacts plus the totals a list UI needs.
type Page struct {
	Items  []Contact `json:"items"`
	Total  int       `json:"total"`
	Limit  int       `json:"limit"`
	Offset int       `json:"offset"`
}

// Service holds the contacts business logic.
type Service struct {
	store *store
}

func newService(pool *pgxpool.Pool) *Service {
	return &Service{store: &store{pool: pool}}
}

// List returns one org-scoped page of contacts, newest first.
func (s *Service) List(ctx context.Context, orgID string, limit, offset int) (Page, error) {
	limit, offset = clampPage(limit, offset)

	items, err := s.store.list(ctx, orgID, limit, offset)
	if err != nil {
		return Page{}, err
	}
	total, err := s.store.count(ctx, orgID)
	if err != nil {
		return Page{}, err
	}
	return Page{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

// Get returns a single contact from the caller's org.
func (s *Service) Get(ctx context.Context, orgID, id string) (Contact, error) {
	return s.store.get(ctx, orgID, id)
}

// Create validates and stores a new contact.
func (s *Service) Create(ctx context.Context, orgID string, in Input) (Contact, error) {
	in, err := s.prepare(ctx, orgID, in)
	if err != nil {
		return Contact{}, err
	}
	return s.store.create(ctx, orgID, in)
}

// Update replaces a contact's fields. Absent optional fields clear their column,
// so this is a full replacement (PUT), not a merge.
func (s *Service) Update(ctx context.Context, orgID, id string, in Input) (Contact, error) {
	in, err := s.prepare(ctx, orgID, in)
	if err != nil {
		return Contact{}, err
	}
	return s.store.update(ctx, orgID, id, in)
}

// Delete removes a contact from the caller's org.
func (s *Service) Delete(ctx context.Context, orgID, id string) error {
	return s.store.delete(ctx, orgID, id)
}

// prepare normalizes and validates input, and confirms any referenced account
// belongs to the caller's org.
func (s *Service) prepare(ctx context.Context, orgID string, in Input) (Input, error) {
	in = normalize(in)
	if err := validate(in); err != nil {
		return Input{}, err
	}
	if in.AccountID != nil {
		ok, err := s.store.accountInOrg(ctx, orgID, *in.AccountID)
		if err != nil {
			return Input{}, err
		}
		if !ok {
			return Input{}, ErrAccountNotFound
		}
	}
	return in, nil
}

// normalize trims whitespace, lower-cases the email, and turns blank optional
// fields into NULL rather than empty strings.
func normalize(in Input) Input {
	in.FirstName = strings.TrimSpace(in.FirstName)
	in.LastName = strings.TrimSpace(in.LastName)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.Phone = trimmedOrNil(in.Phone)
	in.AccountID = trimmedOrNil(in.AccountID)
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

// validate mirrors the client-side rules in shared/schemas (contactSchema); the
// server repeats them because a client is not a trust boundary.
func validate(in Input) error {
	switch {
	case in.FirstName == "":
		return invalid("first name is required")
	case in.LastName == "":
		return invalid("last name is required")
	case len(in.FirstName) > 100 || len(in.LastName) > 100:
		return invalid("names must be 100 characters or fewer")
	case in.Email == "":
		return invalid("email is required")
	case !strings.Contains(in.Email, "@") || strings.ContainsAny(in.Email, " \t"):
		return invalid("a valid email is required")
	case len(in.Email) > 255:
		return invalid("email must be 255 characters or fewer")
	}
	if in.Phone != nil && len(*in.Phone) > 40 {
		return invalid("phone must be 40 characters or fewer")
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

// IsValidation reports whether err is a client input error (→ 400).
func IsValidation(err error) bool {
	var ve ValidationError
	return errors.As(err, &ve)
}
