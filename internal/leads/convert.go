package leads

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-crm/services/internal/delivery"
	"github.com/go-crm/services/pkg/apperr"
	"github.com/go-crm/services/pkg/database"
	"github.com/go-crm/services/pkg/middleware"
	"github.com/jackc/pgx/v5"
)

// ErrAlreadyConverted means the lead has already produced a deal.
var ErrAlreadyConverted = errors.New("lead has already been converted")

// ConvertInput is the convert dialog payload. Supports both Deal form and Convert dialog fields.
type ConvertInput struct {
	DealTitle         *string    `json:"dealTitle"`
	Title             *string    `json:"title"`
	Amount            *float64   `json:"amount"`
	ExpectedCloseDate *time.Time `json:"expectedCloseDate"`
	CallNotes         *string    `json:"callNotes"`
	Description       *string    `json:"description"`
	DealStage         *string    `json:"dealStage"`
	Stage             *string    `json:"stage"`
	OwnerUserID       *string    `json:"ownerUserId"`
	AccountID         *string    `json:"accountId"`
	Products          *string    `json:"products"`
	TotalCameras      *int       `json:"totalCameras"`
	Location          *string    `json:"location"`
}

// Conversion is what a conversion produced.
type Conversion struct {
	LeadID         string `json:"leadId"`
	ContactID      string `json:"contactId"`
	DealID         string `json:"dealId"`
	AccountID      string `json:"accountId"`
	ContactCreated bool   `json:"contactCreated"`
	CallNotes      string `json:"-"`
}

const defaultDealStage = "discovery"

// deliveryStage is the point at which a deal acquires a row in the client delivery tracker.
const deliveryStage = "delivery"

// validDealStages mirrors deals.Stages.
var validDealStages = map[string]bool{
	"discovery": true, "site_assessment": true, "quote_sent": true,
	"negotiation": true, "delivery": true, "post_delivery": true, "won": true,
}

func normalizeDealStage(raw string) string {
	s := strings.TrimSpace(strings.ToLower(raw))
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "-", "_")
	switch s {
	case "prospect", "lead":
		return "discovery"
	case "proposal":
		return "quote_sent"
	case "qualified":
		return "site_assessment"
	}
	return s
}

// Convert turns a lead into a deal, and marks the lead converted.
func (s *Service) Convert(ctx context.Context, orgID, leadID string, in ConvertInput) (Conversion, error) {
	stageStr := defaultDealStage
	if in.Stage != nil && *in.Stage != "" {
		stageStr = *in.Stage
	} else if in.DealStage != nil && *in.DealStage != "" {
		stageStr = *in.DealStage
	}
	norm := normalizeDealStage(stageStr)
	if !validDealStages[norm] {
		return Conversion{}, apperr.Invalid("unknown deal stage %q", stageStr)
	}

	if in.Amount != nil && (*in.Amount < 0 || *in.Amount > 1e12) {
		return Conversion{}, apperr.Invalid("amount must be between 0 and 1,000,000,000,000")
	}

	rawTitle := in.Title
	if rawTitle == nil {
		rawTitle = in.DealTitle
	}
	if rawTitle != nil && len(strings.TrimSpace(*rawTitle)) > 160 {
		return Conversion{}, apperr.Invalid("deal name must be 160 characters or fewer")
	}

	notes := in.Description
	if notes == nil {
		notes = in.CallNotes
	}
	if notes != nil && len(*notes) > 5000 {
		return Conversion{}, apperr.Invalid("notes must be 5000 characters or fewer")
	}

	return s.store.convert(ctx, orgID, leadID, in, norm)
}

func (s *store) convert(
	ctx context.Context, orgID, leadID string, in ConvertInput, stage string,
) (Conversion, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Conversion{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Claim the lead and read its current fields.
	var (
		contactName     string
		email           *string
		phone           *string
		value           *float64
		owner           *string
		leadAccountID   *string
		contactID       *string
		leadNotes       *string
		leadLocation    *string
		productInterest *string
	)

	err = tx.QueryRow(ctx,
		`UPDATE leads
		 SET status = 'converted', next_follow_up_date = NULL, updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL AND status <> 'converted'
		 RETURNING COALESCE(contact_name, ''), email, phone, value_estimate,
		           assigned_to::text, account_id::text, contact_id::text,
		           notes, location, product_interest`,
		leadID,
	).Scan(
		&contactName, &email, &phone, &value,
		&owner, &leadAccountID, &contactID,
		&leadNotes, &leadLocation, &productInterest,
	)

	if errors.Is(err, pgx.ErrNoRows) || database.IsInvalidTextRepr(err) {
		return Conversion{}, s.explainConvertMiss(ctx, orgID, leadID)
	}
	if err != nil {
		return Conversion{}, err
	}

	// 1. Determine Account
	var accountID *string
	if in.AccountID != nil && strings.TrimSpace(*in.AccountID) != "" {
		trimmed := strings.TrimSpace(*in.AccountID)
		accountID = &trimmed
	} else if leadAccountID != nil {
		accountID = leadAccountID
	}

	// 2. Determine Owner
	if in.OwnerUserID != nil && strings.TrimSpace(*in.OwnerUserID) != "" {
		trimmed := strings.TrimSpace(*in.OwnerUserID)
		owner = &trimmed
	} else if owner == nil {
		var defaultOwner string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM profiles ORDER BY created_at ASC LIMIT 1`).Scan(&defaultOwner); err == nil {
			owner = &defaultOwner
		}
	}

	// If account is still not known, try to match or create from company name if available
	rawTitle := in.Title
	if rawTitle == nil {
		rawTitle = in.DealTitle
	}

	var accountName *string
	if accountID != nil {
		var accName string
		if err := tx.QueryRow(ctx, `SELECT name FROM accounts WHERE id = $1`, *accountID).Scan(&accName); err == nil {
			accountName = &accName
		}
	}

	title := dealTitle(rawTitle, accountName, contactName, nil)
	if accountID == nil {
		var foundAcc string
		err := tx.QueryRow(ctx,
			`SELECT id::text FROM accounts WHERE lower(trim(name)) = lower(trim($1)) AND deleted_at IS NULL LIMIT 1`,
			title,
		).Scan(&foundAcc)
		if err == nil {
			accountID = &foundAcc
		} else if owner != nil {
			// Create account if needed
			var newAccID string
			if insErr := tx.QueryRow(ctx,
				`INSERT INTO accounts (name, owner_id) VALUES ($1, $2) RETURNING id::text`,
				title, *owner,
			).Scan(&newAccID); insErr == nil {
				accountID = &newAccID
			}
		}
	}

	// 3. Amount
	amount := 0.0
	if in.Amount != nil {
		amount = *in.Amount
	} else if value != nil {
		amount = *value
	}

	// 4. Notes / Description
	notes := ""
	if in.Description != nil && strings.TrimSpace(*in.Description) != "" {
		notes = strings.TrimSpace(*in.Description)
	} else if in.CallNotes != nil && strings.TrimSpace(*in.CallNotes) != "" {
		notes = strings.TrimSpace(*in.CallNotes)
	} else if leadNotes != nil {
		notes = strings.TrimSpace(*leadNotes)
	}

	// 5. Products, Location, TotalCameras
	var products *string = in.Products
	if (products == nil || *products == "") && productInterest != nil {
		products = productInterest
	}

	var location *string = in.Location
	if (location == nil || *location == "") && leadLocation != nil {
		location = leadLocation
	}

	totalCameras := in.TotalCameras

	// 6. Insert into deals
	var dealID string
	err = tx.QueryRow(ctx,
		`INSERT INTO deals
		   (title, notes, amount, stage, owner_id, primary_contact_id,
		    account_id, lead_id, expected_close_date,
		    total_cameras, location, products)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 RETURNING id::text`,
		title, notes, amount, stage, owner, contactID,
		accountID, leadID, in.ExpectedCloseDate,
		totalCameras, location, products,
	).Scan(&dealID)
	if err != nil {
		return Conversion{}, err
	}

	// 7. Update lead account_id if we have one
	if accountID != nil {
		_, _ = tx.Exec(ctx,
			`UPDATE leads SET account_id = COALESCE(account_id, $2) WHERE id = $1`,
			leadID, *accountID,
		)
	}

	if err := tx.Commit(ctx); err != nil {
		return Conversion{}, err
	}

	// 8. Sync Delivery tracker if stage is delivery
	userID := middleware.UserID(ctx)
	if stage == deliveryStage {
		_, _ = delivery.EnsureRowForDeal(ctx, s.pool, orgID, dealID, userID)
	}
	_ = delivery.SyncFromDeal(ctx, s.pool, dealID, delivery.DealFields{
		Products:     products,
		Location:     location,
		TotalCameras: totalCameras,
	})

	return Conversion{
		LeadID:         leadID,
		ContactID:      derefID(contactID),
		DealID:         dealID,
		AccountID:      derefID(accountID),
		ContactCreated: false,
		CallNotes:      notes,
	}, nil
}

// explainConvertMiss tells "no such lead" apart from "already converted".
func (s *store) explainConvertMiss(ctx context.Context, _, leadID string) error {
	var status string
	err := s.pool.QueryRow(ctx,
		`SELECT status FROM leads WHERE id = $1 AND deleted_at IS NULL`, leadID,
	).Scan(&status)

	switch {
	case errors.Is(err, pgx.ErrNoRows), database.IsInvalidTextRepr(err):
		return ErrNotFound
	case err != nil:
		return err
	case status == "converted":
		return ErrAlreadyConverted
	default:
		return ErrNotFound
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
	if strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	return "New Deal"
}

func derefID(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
