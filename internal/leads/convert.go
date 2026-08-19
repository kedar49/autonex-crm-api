package leads

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrAlreadyConverted means the lead has already produced a deal.
var ErrAlreadyConverted = errors.New("lead has already been converted")

// ConvertInput is the brief's convert dialog (§3.4): everything pre-filled from
// the lead, with the four things a person actually types.
type ConvertInput struct {
	DealTitle         *string    `json:"dealTitle"`
	Amount            *float64   `json:"amount"`
	ExpectedCloseDate *time.Time `json:"expectedCloseDate"`
	// CallNotes become a logged activity, not a field on the deal — "key points
	// from the call" is history, and history belongs on the timeline.
	CallNotes *string `json:"callNotes"`
	DealStage *string `json:"dealStage"`
}

// Conversion is what a conversion produced.
type Conversion struct {
	LeadID    string `json:"leadId"`
	ContactID string `json:"contactId"`
	DealID    string `json:"dealId"`
	AccountID string `json:"accountId"`
	// ContactCreated is false when the lead's existing contact was reused.
	ContactCreated bool   `json:"contactCreated"`
	CallNotes      string `json:"-"`
}

// defaultDealStage is the first stage of the *deal* pipeline. A converted lead
// starts at the beginning of the deal board rather than being guessed into the
// middle of it.
const defaultDealStage = "lead"

// validDealStages mirrors deals.Stages. Duplicated deliberately: importing the
// deals package here would make two domain modules mutually dependent, and the
// database CHECK constraint is the real enforcement either way.
var validDealStages = map[string]bool{
	"lead": true, "qualified": true, "proposal": true, "won": true, "lost": true,
}

// Convert turns a lead into a contact plus a deal, and marks the lead converted.
//
// All the writes happen in one transaction: a conversion that created a deal but
// left the lead open — or created a contact and then failed — would be worse than
// not converting at all.
//
// Boundary note: this is the one place the leads module writes to another
// module's tables. Doing it "properly" through the contacts and deals services
// would need a shared unit-of-work, since each store owns its own pool and a
// cross-module transaction can't otherwise be expressed.
func (s *Service) Convert(ctx context.Context, orgID, leadID string, in ConvertInput) (Conversion, error) {
	stage := defaultDealStage
	if in.DealStage != nil && *in.DealStage != "" {
		if !validDealStages[*in.DealStage] {
			return Conversion{}, invalid("unknown deal stage %q", *in.DealStage)
		}
		stage = *in.DealStage
	}
	if in.Amount != nil && (*in.Amount < 0 || *in.Amount > 1e12) {
		return Conversion{}, invalid("amount must be between 0 and 1,000,000,000,000")
	}
	if in.DealTitle != nil && len(strings.TrimSpace(*in.DealTitle)) > 160 {
		return Conversion{}, invalid("deal name must be 160 characters or fewer")
	}
	if in.CallNotes != nil && len(*in.CallNotes) > 5000 {
		return Conversion{}, invalid("call notes must be 5000 characters or fewer")
	}

	return s.store.convert(ctx, orgID, leadID, in, stage)
}

func (s *store) convert(
	ctx context.Context, orgID, leadID string, in ConvertInput, stage string,
) (Conversion, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Conversion{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Claim the lead. `converted_at IS NULL` makes this the idempotency guard:
	// two concurrent requests can't both go on to create a deal.
	var (
		firstName string
		lastName  *string
		email     *string
		phone     *string
		company   *string
		value     *float64
		owner     *string
		accountID *string
		contactID *string
	)
	err = tx.QueryRow(ctx,
		`UPDATE leads
		 SET stage = 'converted', converted_at = now(), follow_up_at = NULL, updated_at = now()
		 WHERE org_id = $1 AND id = $2 AND converted_at IS NULL
		 RETURNING first_name, last_name, email, phone, company, value,
		           owner_user_id::text, account_id::text, contact_id::text`,
		orgID, leadID,
	).Scan(&firstName, &lastName, &email, &phone, &company, &value, &owner, &accountID, &contactID)

	if errors.Is(err, pgx.ErrNoRows) || isPgCode(err, pgInvalidTextRepr) {
		return Conversion{}, s.explainConvertMiss(ctx, orgID, leadID)
	}
	if err != nil {
		return Conversion{}, err
	}

	// The company: use the lead's link, else match the free-text name against an
	// existing account, else create one. A deal without a company is hard to work.
	if accountID == nil && company != nil && strings.TrimSpace(*company) != "" {
		name := strings.TrimSpace(*company)
		var found string
		err = tx.QueryRow(ctx,
			`SELECT id::text FROM accounts WHERE org_id = $1 AND lower(name) = lower($2) LIMIT 1`,
			orgID, name).Scan(&found)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Conversion{}, err
		}
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.QueryRow(ctx,
				`INSERT INTO accounts (org_id, name) VALUES ($1, $2) RETURNING id::text`,
				orgID, name).Scan(&found); err != nil {
				return Conversion{}, err
			}
		}
		accountID = &found
	}

	// The person: the lead's linked contact, else one matching the email, else new.
	created := false
	if contactID == nil {
		if email != nil {
			var found string
			err = tx.QueryRow(ctx,
				`SELECT id::text FROM contacts WHERE org_id = $1 AND lower(email) = lower($2)`,
				orgID, *email).Scan(&found)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return Conversion{}, err
			}
			if err == nil {
				contactID = &found
			}
		}
		if contactID == nil {
			var newID string
			if err := tx.QueryRow(ctx,
				`INSERT INTO contacts (org_id, first_name, last_name, email, phone, account_id)
				 VALUES ($1, $2, $3, $4, $5, $6)
				 RETURNING id::text`,
				orgID, firstName, lastName, email, phone, accountID,
			).Scan(&newID); err != nil {
				return Conversion{}, err
			}
			contactID = &newID
			created = true
		}
	}

	title := dealTitle(in.DealTitle, company, firstName, lastName)
	amount := 0.0
	if in.Amount != nil {
		amount = *in.Amount
	} else if value != nil {
		amount = *value
	}

	var dealID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO deals
		   (org_id, title, amount, stage, owner_user_id, contact_id, account_id,
		    expected_close_date, position)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8,
		         COALESCE((SELECT max(position) + 1000 FROM deals
		                   WHERE org_id = $1 AND stage = $4), 0))
		 RETURNING id::text`,
		orgID, title, amount, stage, owner, contactID, accountID, in.ExpectedCloseDate,
	).Scan(&dealID); err != nil {
		return Conversion{}, err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE leads SET converted_deal_id = $3, converted_contact_id = $4, account_id = $5
		 WHERE org_id = $1 AND id = $2`,
		orgID, leadID, dealID, contactID, accountID); err != nil {
		return Conversion{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Conversion{}, err
	}

	notes := ""
	if in.CallNotes != nil {
		notes = strings.TrimSpace(*in.CallNotes)
	}
	return Conversion{
		LeadID: leadID, ContactID: *contactID, DealID: dealID,
		AccountID: derefID(accountID), ContactCreated: created, CallNotes: notes,
	}, nil
}

// explainConvertMiss tells "no such lead" apart from "already converted".
func (s *store) explainConvertMiss(ctx context.Context, orgID, leadID string) error {
	var convertedAt *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT converted_at FROM leads WHERE org_id = $1 AND id = $2`, orgID, leadID,
	).Scan(&convertedAt)

	switch {
	case errors.Is(err, pgx.ErrNoRows), isPgCode(err, pgInvalidTextRepr):
		return ErrNotFound
	case err != nil:
		return err
	default:
		// Exists but the claim missed: it is converted, or a concurrent
		// conversion committed between the two statements.
		return ErrAlreadyConverted
	}
}

// dealTitle prefers an explicit title, then the company, then the person's name.
func dealTitle(override, company *string, firstName string, lastName *string) string {
	if override != nil {
		if t := strings.TrimSpace(*override); t != "" {
			return t
		}
	}
	if company != nil {
		if t := strings.TrimSpace(*company); t != "" {
			return t
		}
	}
	name := firstName
	if lastName != nil && *lastName != "" {
		name = fmt.Sprintf("%s %s", firstName, *lastName)
	}
	return name
}

func derefID(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
