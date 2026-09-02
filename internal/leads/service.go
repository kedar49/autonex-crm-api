package leads

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Stages is the lead lifecycle, per the redesign brief §3.2.
//
//	new → initial count → deck sent → call scheduled → call done → proposal sent
//	    → [closed | not interested]
//
// Outreach only. A lead does not "close" — it hands off to a deal, which is why
// "proposal sent" and "closed" live in the deal pipeline and not here.
// The vocabulary is the one the database enforces (leads_status_check), not
// the one the original brief described — the rows in this deployment use
// space-separated names and a different pipeline.
var Stages = []string{
	"new", "initial count", "deck sent", "call scheduled",
	"call done", "proposal sent", "closed", "not interested",
}

// Terminal stages: work has finished, one way or the other.
var terminal = map[string]bool{"closed": true, "not interested": true}

// Filters the list accepts beyond a plain stage name.
const (
	FilterOverdue  = "overdue"
	FilterDueToday = "due_today"
	FilterOpen     = "open"
)

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

// Input is the writable shape of a lead.
type Input struct {
	FirstName   string     `json:"firstName"`
	LastName    *string    `json:"lastName"`
	Title       *string    `json:"title"`
	Email       *string    `json:"email"`
	Phone       *string    `json:"phone"`
	LinkedIn    *string    `json:"linkedinUrl"`
	Company     *string    `json:"company"`
	AccountID   *string    `json:"accountId"`
	ContactID   *string    `json:"contactId"`
	Source      *string    `json:"source"`
	Notes       *string    `json:"notes"`
	Value       *float64   `json:"value"`
	Stage       string     `json:"stage"`
	OwnerUserID *string    `json:"ownerUserId"`
	FollowUpAt  *time.Time `json:"followUpAt"`
}

// Advance is one step along the lifecycle, optionally rescheduling the next touch.
type Advance struct {
	ToStage    string     `json:"toStage"`
	FollowUpAt *time.Time `json:"followUpAt"`
	// ClearFollowUp distinguishes "leave the date alone" from "there is nothing
	// left to chase", which a nil date alone cannot express.
	ClearFollowUp bool    `json:"clearFollowUp"`
	Note          *string `json:"note"`
}

// Page is one page of leads plus the counts the funnel strip and filter pills need.
type Page struct {
	Items  []Lead         `json:"items"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
	Counts map[string]int `json:"counts"`
	Stages []string       `json:"stages"`
}

// Service holds the leads business logic.
type Service struct {
	store *store
}

// NewService exposes the service to sibling modules (the dashboard reads stats).
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: &store{pool: pool}}
}

// List returns one org-scoped page, sorted by urgency.
func (s *Service) List(ctx context.Context, orgID, filter string, limit, offset int) (Page, error) {
	if filter != "" && !ValidFilter(filter) {
		return Page{}, invalid("unknown filter %q", filter)
	}
	limit, offset = clampPage(limit, offset)

	items, err := s.store.list(ctx, orgID, filter, limit, offset)
	if err != nil {
		return Page{}, err
	}
	total, err := s.store.count(ctx, orgID, filter)
	if err != nil {
		return Page{}, err
	}
	counts, err := s.store.counts(ctx, orgID)
	if err != nil {
		return Page{}, err
	}
	return Page{
		Items: items, Total: total, Limit: limit, Offset: offset,
		Counts: counts, Stages: Stages,
	}, nil
}

// Get returns a single lead from the caller's org.
func (s *Service) Get(ctx context.Context, orgID, id string) (Lead, error) {
	return s.store.get(ctx, orgID, id)
}

// Create validates and stores a new lead.
func (s *Service) Create(ctx context.Context, orgID string, in Input) (Lead, error) {
	in, err := s.prepare(ctx, orgID, in)
	if err != nil {
		return Lead{}, err
	}
	id, err := s.store.create(ctx, orgID, in)
	if err != nil {
		return Lead{}, err
	}
	return s.store.get(ctx, orgID, id)
}

// Update replaces a lead's fields (a full PUT, not a merge).
func (s *Service) Update(ctx context.Context, orgID, id string, in Input) (Lead, error) {
	in, err := s.prepare(ctx, orgID, in)
	if err != nil {
		return Lead{}, err
	}
	if err := s.store.update(ctx, orgID, id, in); err != nil {
		return Lead{}, err
	}
	return s.store.get(ctx, orgID, id)
}

// AdvanceStage applies one of the list's contextual actions.
func (s *Service) AdvanceStage(ctx context.Context, orgID, id string, adv Advance) (Lead, error) {
	switch {
	case !ValidStage(adv.ToStage):
		return Lead{}, invalid("unknown stage %q", adv.ToStage)
	case adv.ToStage == "converted":
		// Converting creates a contact and a deal, so it can't be a stage edit.
		return Lead{}, invalid("use the convert action to turn a lead into a deal")
	}
	if adv.Note != nil && len(*adv.Note) > 5000 {
		return Lead{}, invalid("note must be 5000 characters or fewer")
	}

	if err := s.store.advance(ctx, orgID, id, adv.ToStage, adv.FollowUpAt, adv.ClearFollowUp); err != nil {
		return Lead{}, err
	}
	return s.store.get(ctx, orgID, id)
}

// Delete removes a lead from the caller's org.
func (s *Service) Delete(ctx context.Context, orgID, id string) error {
	return s.store.delete(ctx, orgID, id)
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

// ValidFilter reports whether the list accepts this filter value.
func ValidFilter(filter string) bool {
	switch filter {
	case FilterOverdue, FilterDueToday, FilterOpen:
		return true
	}
	return ValidStage(filter)
}

// NextStage is the natural next step for the list's Action column. The UI picks
// the label; the sequence itself is domain, so it lives here.
func NextStage(stage string) string {
	switch stage {
	case "new":
		return "initial count"
	case "initial count":
		return "deck sent"
	case "deck sent":
		return "call scheduled"
	case "call scheduled":
		return "call done"
	case "call done":
		return "proposal sent"
	case "proposal sent":
		return "closed"
	default:
		// Terminal stages have nowhere to go.
		return ""
	}
}

// StageLabel renders a stage for humans (timeline entries, notifications).
func StageLabel(stage string) string {
	switch stage {
	case "new":
		return "New"
	case "contacted":
		return "Contacted"
	case "replied":
		return "Replied"
	case "call_booked":
		return "Call booked"
	case "call_done":
		return "Call done"
	case "converted":
		return "Converted"
	case "dropped":
		return "Dropped"
	default:
		return stage
	}
}

// IsTerminal reports whether a lead has finished its outreach life.
func IsTerminal(stage string) bool { return terminal[stage] }

func (s *Service) prepare(ctx context.Context, orgID string, in Input) (Input, error) {
	in = normalize(in)
	if err := validate(in); err != nil {
		return Input{}, err
	}

	for _, ref := range []struct {
		table string
		id    *string
	}{
		{"users", in.OwnerUserID},
		{"companies", in.AccountID},
		{"contacts", in.ContactID},
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

// normalize trims text, lower-cases the email, blanks become NULL, and an
// omitted stage defaults to the start of the pipeline.
func normalize(in Input) Input {
	in.FirstName = strings.TrimSpace(in.FirstName)
	in.LastName = trimmedOrNil(in.LastName)
	in.Title = trimmedOrNil(in.Title)
	in.Phone = trimmedOrNil(in.Phone)
	in.Company = trimmedOrNil(in.Company)
	in.Source = trimmedOrNil(in.Source)
	in.Notes = trimmedOrNil(in.Notes)
	in.AccountID = trimmedOrNil(in.AccountID)
	in.ContactID = trimmedOrNil(in.ContactID)
	in.OwnerUserID = trimmedOrNil(in.OwnerUserID)

	if e := trimmedOrNil(in.Email); e != nil {
		lower := strings.ToLower(*e)
		in.Email = &lower
	} else {
		in.Email = nil
	}

	// A bare profile path is not a link; give it a scheme so it can be an href.
	if u := trimmedOrNil(in.LinkedIn); u != nil {
		link := *u
		if !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") {
			link = "https://" + link
		}
		in.LinkedIn = &link
	} else {
		in.LinkedIn = nil
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
// enough. Everything else is filled in as the lead progresses, so demanding an
// email up front would just teach people to type junk.
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
	if in.Title != nil && len(*in.Title) > 120 {
		return invalid("title must be 120 characters or fewer")
	}
	if in.LinkedIn != nil && len(*in.LinkedIn) > 255 {
		return invalid("LinkedIn URL must be 255 characters or fewer")
	}
	if in.Value != nil && (*in.Value < 0 || *in.Value > 1e12) {
		return invalid("value must be between 0 and 1,000,000,000,000")
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
