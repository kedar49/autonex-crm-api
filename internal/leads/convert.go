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

// ConvertInput lets the caller override what the new deal looks like. Everything
// is optional — the defaults are derived from the lead.
type ConvertInput struct {
	DealTitle         *string    `json:"dealTitle"`
	Amount            *float64   `json:"amount"`
	DealStage         *string    `json:"dealStage"`
	ExpectedCloseDate *time.Time `json:"expectedCloseDate"`
}

// Conversion is what a conversion produced. Ids only: returning the full deal
// would mean importing the deals module's types into leads, and the client
// refetches both boards anyway.
type Conversion struct {
	LeadID    string `json:"leadId"`
	ContactID string `json:"contactId"`
	DealID    string `json:"dealId"`
	/// ContactCreated is false when an existing contact with the lead's email was
	/// reused rather than a new one inserted.
	ContactCreated bool `json:"contactCreated"`
}

// defaultDealStage is the first stage of the *deal* pipeline. A converted lead
// starts at the beginning of the deal board rather than being guessed into the
// middle of it — deliberately the deals module's first stage, not the leads one.
const defaultDealStage = "lead"

// validDealStages mirrors deals.Stages. Duplicated deliberately: importing the
// deals package here would make two domain modules mutually dependent, and the
// database CHECK constraint is the real enforcement either way.
var validDealStages = map[string]bool{
	"lead": true, "qualified": true, "proposal": true, "won": true, "lost": true,
}

// Convert turns a lead into a contact plus a deal, and marks the lead won.
//
// All three writes happen in one transaction: a conversion that created a deal
// but left the lead open — or created a contact and then failed — would be worse
// than not converting at all.
//
// Boundary note: this is the one place the leads module writes to another
// module's tables. Doing it "properly" through the contacts and deals services
// would need a shared unit-of-work abstraction, since each store owns its own
// pool and a cross-module transaction can't otherwise be expressed. The SQL is
// kept here, small and explicit, rather than inventing that machinery for a
// single use.
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
		return Conversion{}, invalid("title must be 160 characters or fewer")
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
	// two concurrent requests can't both go on to create a deal, because the
	// loser updates zero rows.
	var (
		firstName string
		lastName  *string
		email     *string
		phone     *string
		company   *string
		value     *float64
		owner     *string
	)
	err = tx.QueryRow(ctx,
		`UPDATE leads
		 SET stage = 'won', converted_at = now(), updated_at = now()
		 WHERE org_id = $1 AND id = $2 AND converted_at IS NULL
		 RETURNING first_name, last_name, email, phone, company, value, owner_user_id::text`,
		orgID, leadID,
	).Scan(&firstName, &lastName, &email, &phone, &company, &value, &owner)

	if errors.Is(err, pgx.ErrNoRows) || isPgCode(err, pgInvalidTextRepr) {
		// Either it doesn't exist (in this org) or it is already converted —
		// distinguish only on this failure path, so the happy path stays one query.
		return Conversion{}, s.explainConvertMiss(ctx, orgID, leadID)
	}
	if err != nil {
		return Conversion{}, err
	}

	// Reuse an existing contact with the same email rather than creating a
	// duplicate — the partial unique index would reject it anyway, and silently
	// linking to the person already on file is what the user means.
	var contactID string
	created := false
	if email != nil {
		err = tx.QueryRow(ctx,
			`SELECT id::text FROM contacts WHERE org_id = $1 AND lower(email) = lower($2)`,
			orgID, *email).Scan(&contactID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Conversion{}, err
		}
	}
	if contactID == "" {
		if err := tx.QueryRow(ctx,
			`INSERT INTO contacts (org_id, first_name, last_name, email, phone)
			 VALUES ($1, $2, $3, $4, $5)
			 RETURNING id::text`,
			orgID, firstName, lastName, email, phone,
		).Scan(&contactID); err != nil {
			return Conversion{}, err
		}
		created = true
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
		   (org_id, title, amount, stage, owner_user_id, contact_id, expected_close_date, position)
		 VALUES ($1, $2, $3, $4, $5, $6, $7,
		         COALESCE((SELECT max(position) + 1000 FROM deals
		                   WHERE org_id = $1 AND stage = $4), 0))
		 RETURNING id::text`,
		orgID, title, amount, stage, owner, contactID, in.ExpectedCloseDate,
	).Scan(&dealID); err != nil {
		return Conversion{}, err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE leads SET converted_deal_id = $3, converted_contact_id = $4
		 WHERE org_id = $1 AND id = $2`,
		orgID, leadID, dealID, contactID); err != nil {
		return Conversion{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Conversion{}, err
	}
	return Conversion{
		LeadID: leadID, ContactID: contactID, DealID: dealID, ContactCreated: created,
	}, nil
}

// explainConvertMiss tells "no such lead" apart from "already converted", so the
// client can show something useful. Runs only when the claim failed.
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
	case convertedAt != nil:
		return ErrAlreadyConverted
	default:
		// Exists, not converted, yet the claim missed — a concurrent conversion
		// committed between the two statements.
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
