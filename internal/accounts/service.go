package accounts

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pagination bounds for list requests, matching contacts.
const (
	defaultLimit = 25
	maxLimit     = 100
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

// Input is the writable shape of an account. Only the name is required — the rest
// gets filled in as you learn about the company.
type Input struct {
	Name        string  `json:"name"`
	Website     *string `json:"website"`
	Industry    *string `json:"industry"`
	Phone       *string `json:"phone"`
	Notes       *string `json:"notes"`
	OwnerUserID *string `json:"ownerUserId"`
}

// Page is one page of accounts plus the totals a list UI needs.
type Page struct {
	Items  []Account `json:"items"`
	Total  int       `json:"total"`
	Limit  int       `json:"limit"`
	Offset int       `json:"offset"`
}

// Service holds the accounts business logic.
type Service struct {
	store *store
}

func newService(pool *pgxpool.Pool) *Service {
	return &Service{store: &store{pool: pool}}
}

// List returns one org-scoped page of accounts, newest first.
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

// Get returns a single account from the caller's org.
func (s *Service) Get(ctx context.Context, orgID, id string) (Account, error) {
	return s.store.get(ctx, orgID, id)
}

// GetFullProfile returns the full company profile including VIGIL configuration, plant locations, and linked deals/quotes/invoices/contacts.
func (s *Service) GetFullProfile(ctx context.Context, orgID, id string) (FullCompanyProfilePayload, error) {
	return s.store.getFullProfile(ctx, orgID, id)
}

// UpdateFullProfile updates account details and upserts the company profile extension data.
func (s *Service) UpdateFullProfile(ctx context.Context, orgID, id string, in ProfileInput) (FullCompanyProfilePayload, error) {
	preparedBase, err := s.prepare(ctx, orgID, Input{
		Name:        in.Name,
		Website:     in.Website,
		Industry:    in.Industry,
		Phone:       in.Phone,
		Notes:       in.Notes,
		OwnerUserID: in.OwnerUserID,
	})
	if err != nil {
		return FullCompanyProfilePayload{}, err
	}
	in.Name = preparedBase.Name
	in.Website = preparedBase.Website
	in.Industry = preparedBase.Industry
	in.Phone = preparedBase.Phone
	in.Notes = preparedBase.Notes
	in.OwnerUserID = preparedBase.OwnerUserID

	in.Tagline = trimmedOrNil(in.Tagline)
	in.Description = trimmedOrNil(in.Description)
	in.PrimaryColor = trimmedOrNil(in.PrimaryColor)
	in.BannerURL = trimmedOrNil(in.BannerURL)
	in.AMCStatus = trimmedOrNil(in.AMCStatus)
	in.AMCStartDate = trimmedOrNil(in.AMCStartDate)
	in.AMCEndDate = trimmedOrNil(in.AMCEndDate)

	return s.store.upsertProfile(ctx, orgID, id, in)
}

// Create validates and stores a new account.
func (s *Service) Create(ctx context.Context, orgID string, in Input) (Account, error) {
	in, err := s.prepare(ctx, orgID, in)
	if err != nil {
		return Account{}, err
	}
	return s.store.create(ctx, orgID, in)
}

// Update replaces an account's fields (a full PUT, not a merge).
func (s *Service) Update(ctx context.Context, orgID, id string, in Input) (Account, error) {
	in, err := s.prepare(ctx, orgID, in)
	if err != nil {
		return Account{}, err
	}
	return s.store.update(ctx, orgID, id, in)
}

// Delete removes an account, refusing while contacts or deals still link to it.
func (s *Service) Delete(ctx context.Context, orgID, id string) error {
	return s.store.delete(ctx, orgID, id)
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

// normalize trims text, blanks become NULL, and a bare domain gets a scheme so
// the stored value is a usable href.
func normalize(in Input) Input {
	in.Name = strings.TrimSpace(in.Name)
	in.Industry = trimmedOrNil(in.Industry)
	in.Phone = trimmedOrNil(in.Phone)
	in.Notes = trimmedOrNil(in.Notes)
	in.OwnerUserID = trimmedOrNil(in.OwnerUserID)

	if w := trimmedOrNil(in.Website); w != nil {
		site := *w
		if !strings.HasPrefix(site, "http://") && !strings.HasPrefix(site, "https://") {
			site = "https://" + site
		}
		in.Website = &site
	} else {
		in.Website = nil
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

// validate mirrors the client-side rules in shared/schemas (accountSchema).
func validate(in Input) error {
	switch {
	case in.Name == "":
		return invalid("company name is required")
	case len(in.Name) > 160:
		return invalid("company name must be 160 characters or fewer")
	}
	if in.Website != nil {
		if len(*in.Website) > 255 {
			return invalid("website must be 255 characters or fewer")
		}
		// A scheme is guaranteed by normalize; reject anything that still can't be
		// a host (a space, or nothing after the scheme).
		host := strings.TrimPrefix(strings.TrimPrefix(*in.Website, "https://"), "http://")
		if host == "" || strings.ContainsAny(host, " \t") {
			return invalid("website must be a valid URL")
		}
	}
	if in.Industry != nil && len(*in.Industry) > 80 {
		return invalid("industry must be 80 characters or fewer")
	}
	if in.Phone != nil && len(*in.Phone) > 40 {
		return invalid("phone must be 40 characters or fewer")
	}
	if in.Notes != nil && len(*in.Notes) > 5000 {
		return invalid("notes must be 5000 characters or fewer")
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
