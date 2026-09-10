// Package accounts is the CRM accounts (companies) domain module. An "account"
// here is a company the tenant sells to — not the tenant itself, which is an
// organization (see EXPLAINER §13).
//
// The accounts a lead or deal belongs to live in the `accounts` table:
// contacts.account_id, deals.account_id and leads.account_id all point there.
//
// That table was called `companies` until migration 000004 renamed it. Nothing
// named `companies` exists any more — if you find a reference to it, it is a
// bug, not a second table. Several modules kept querying it for a while and
// returned 500s on any account-linked write as a result.
package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-crm/services/pkg/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	jsonMarshal   = json.Marshal
	jsonUnmarshal = json.Unmarshal
)

var (
	// ErrNotFound means no account with that id exists.
	ErrNotFound = errors.New("account not found")
	// ErrOwnerNotFound means the assignee does not exist.
	ErrOwnerNotFound = errors.New("owner is not a member of this organization")
)

// Account is the module's view of a row, plus the two counts a list needs to be
// useful and the denormalized owner label.
type Account struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Website     *string   `json:"website"`
	Industry    *string   `json:"industry"`
	Phone       *string   `json:"phone"`
	Notes       *string   `json:"notes"`
	OwnerUserID *string   `json:"ownerUserId"`
	OwnerName   *string   `json:"ownerName"`
	OwnerEmail  *string   `json:"ownerEmail"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	// Counts of what hangs off this company. Answering "can I delete this?" and
	// "is this account real?" without a second round-trip per row.
	ContactCount int `json:"contactCount"`
	DealCount    int `json:"dealCount"`
	LeadCount    int `json:"leadCount"`
}

type store struct {
	pool *pgxpool.Pool
}

// The counts are correlated subqueries rather than GROUP BY joins: with two
// different child tables a join would multiply rows and need DISTINCT, and at
// list-page sizes (25) the planner turns these into cheap index lookups.
//
// `accounts` carries a domain rather than a full website URL, and has no phone
// or notes column; those are selected as typed NULLs so the scan positions and
// the JSON contract are unchanged.
const accountColumns = `
	a.id::text, a.name,
	a.domain          AS website,
	a.industry,
	NULL::text        AS phone,
	NULL::text        AS notes,
	a.owner_id::text  AS owner_user_id,
	p.full_name       AS owner_name,
	NULL::text        AS owner_email,
	a.created_at, a.updated_at,
	(SELECT count(*) FROM contacts c WHERE c.account_id = a.id AND c.deleted_at IS NULL),
	(SELECT count(*) FROM deals    d WHERE d.account_id = a.id AND d.deleted_at IS NULL),
	(SELECT count(*) FROM leads    l WHERE l.account_id = a.id AND l.deleted_at IS NULL)`

const accountFrom = ` FROM accounts a LEFT JOIN profiles p ON p.id = a.owner_id `

const searchClause = `
	WHERE a.deleted_at IS NULL
	  AND ($1 = '' OR
	       position($1 in lower(a.name))                    > 0 OR
	       position($1 in lower(coalesce(a.domain, '')))    > 0 OR
	       position($1 in lower(coalesce(a.industry, '')))  > 0)`

func (s *store) list(ctx context.Context, _ string, search string, limit, offset int) ([]Account, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+accountColumns+accountFrom+searchClause+
			` ORDER BY a.created_at DESC, a.id
			 LIMIT $2 OFFSET $3`, search, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Non-nil so an empty page marshals as [] rather than null.
	out := make([]Account, 0, limit)
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *store) count(ctx context.Context, _ string, search string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM accounts a`+searchClause, search).Scan(&n)
	return n, err
}

func (s *store) get(ctx context.Context, _, id string) (Account, error) {
	return scanAccount(s.pool.QueryRow(ctx,
		`SELECT `+accountColumns+accountFrom+`WHERE a.id = $1 AND a.deleted_at IS NULL`, id))
}

// create records a company. Phone and notes have no column here, so they are
// accepted by the API and dropped rather than failing the insert.
func (s *store) create(ctx context.Context, orgID string, in Input) (Account, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`INSERT INTO accounts (name, domain, industry, owner_id)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id::text`,
		in.Name, in.Website, in.Industry, in.OwnerUserID).Scan(&id)
	if err != nil {
		return Account{}, translate(err)
	}
	return s.get(ctx, orgID, id)
}

func (s *store) update(ctx context.Context, orgID, id string, in Input) (Account, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE accounts
		 SET name = $2, domain = $3, industry = $4, owner_id = $5, updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`,
		id, in.Name, in.Website, in.Industry, in.OwnerUserID)
	if err != nil {
		return Account{}, translate(err)
	}
	if tag.RowsAffected() == 0 {
		return Account{}, ErrNotFound
	}
	return s.get(ctx, orgID, id)
}

// delete retires a company and everything filed under it.
//
// It used to refuse whenever a contact or a deal still pointed at the company,
// which made every company anyone had actually worked undeletable. Deleting one
// now takes its contacts, leads, deals, quotes and invoices with it, in a single
// transaction, so the company does not leave a trail of records nothing can
// reach.
//
// Soft throughout, for the reason a hard delete could not work anyway: leads,
// deals, quotes and invoices all reference accounts with no ON DELETE clause, so
// Postgres refused the delete outright. Every read in those modules filters
// deleted_at, so the whole set disappears together and can be brought back by
// clearing the timestamps.
//
// The delivery tracker is the one hard delete here, and the one thing that does
// not come back: those rows have no deleted_at column, and a delivery line for a
// retired company's deal describes work that is no longer happening. They go
// first, while the deals they hang off can still be identified.
func (s *store) delete(ctx context.Context, _, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx,
		`UPDATE accounts SET deleted_at = now(), updated_at = now()
		  WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM delivery_tracker
		  WHERE deal_id IN (SELECT id FROM deals WHERE account_id = $1)`,
		id); err != nil {
		return err
	}

	// Every table that files a record under a company. quote_items and
	// invoice_items hang off their parents and are read through them, so
	// retiring the parent is enough.
	for _, table := range []string{"contacts", "leads", "deals", "quotes", "invoices"} {
		if _, err := tx.Exec(ctx,
			`UPDATE `+table+` SET deleted_at = now()
			  WHERE account_id = $1 AND deleted_at IS NULL`, id); err != nil {
			return translate(err)
		}
	}

	return tx.Commit(ctx)
}

// ownerInOrg reports whether the assignee exists. Owners are profiles in this
// schema, and the deployment is single-tenant, so existence is the whole check.
func (s *store) ownerInOrg(ctx context.Context, _, userID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM profiles WHERE id = $1)`, userID).Scan(&exists)
	if err != nil {
		if database.IsInvalidTextRepr(err) {
			return false, nil
		}
		return false, err
	}
	return exists, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAccount(row rowScanner) (Account, error) {
	var a Account
	err := row.Scan(
		&a.ID, &a.Name, &a.Website, &a.Industry, &a.Phone, &a.Notes,
		&a.OwnerUserID, &a.OwnerName, &a.OwnerEmail, &a.CreatedAt, &a.UpdatedAt,
		&a.ContactCount, &a.DealCount, &a.LeadCount)
	if err != nil {
		return Account{}, translate(err)
	}
	return a, nil
}

func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows), database.IsInvalidTextRepr(err):
		return ErrNotFound
	case database.IsForeignKeyViolation(err):
		return ErrOwnerNotFound
	default:
		return err
	}
}

// --- Company Profile Extensions ---

type PlantLocation struct {
	Name      string `json:"name"`
	City      string `json:"city"`
	Address   string `json:"address,omitempty"`
	SPOCName  string `json:"spocName,omitempty"`
	SPOCPhone string `json:"spocPhone,omitempty"`
}

type HardwareSpecs struct {
	EdgeProcessor string `json:"edgeProcessor,omitempty"`
	CameraCount   int    `json:"cameraCount,omitempty"`
	SpeakerCount  int    `json:"speakerCount,omitempty"`
	NVRMake       string `json:"nvrMake,omitempty"`
}

type CustomSection struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

type CompanyProfile struct {
	AccountID      string          `json:"companyId"`
	Tagline        *string         `json:"tagline"`
	Description    *string         `json:"description"`
	PrimaryColor   string          `json:"primaryColor"`
	BannerURL      *string         `json:"bannerUrl"`
	PlantLocations []PlantLocation `json:"plantLocations"`
	AIDetections   []string        `json:"aiDetections"`
	HardwareSpecs  HardwareSpecs   `json:"hardwareSpecs"`
	AMCStatus      string          `json:"amcStatus"`
	AMCStartDate   *string         `json:"amcStartDate"`
	AMCEndDate     *string         `json:"amcEndDate"`
	AMCValue       float64         `json:"amcValue"`
	CustomSections []CustomSection `json:"customSections"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

type LinkedDeal struct {
	ID                 string  `json:"id"`
	Title              string  `json:"title"`
	Stage              string  `json:"stage"`
	Amount             float64 `json:"amount"`
	Probability        *int    `json:"probability"`
	SiteAssessmentDate *string `json:"siteAssessmentDate"`
	SiteAssessmentLoc  *string `json:"siteAssessmentLocation"`
	ExpectedCloseDate  *string `json:"expectedCloseDate"`
	Remark             *string `json:"remark"`
	// Carried so the company profile's edit dialog round-trips the whole deal.
	// A field missing here is written back as empty when someone saves.
	LeadID *string `json:"leadId"`
	// Mirrors the deal's deployment fields, so the company profile can show and
	// total what was actually sold without a second request per deal.
	TotalCameras *int      `json:"totalCameras"`
	Location     *string   `json:"location"`
	Products     *string   `json:"products"`
	CreatedAt    time.Time `json:"createdAt"`
}

type LinkedQuote struct {
	ID             string    `json:"id"`
	Number         *string   `json:"number"`
	Status         string    `json:"status"`
	Total          float64   `json:"total"`
	Currency       string    `json:"currency"`
	CurrentVersion int       `json:"currentVersion"`
	ValidUntil     *string   `json:"validUntil"`
	CreatedAt      time.Time `json:"createdAt"`
}

type LinkedInvoice struct {
	ID            string    `json:"id"`
	InvoiceNumber *string   `json:"invoiceNumber"`
	Title         *string   `json:"title"`
	Status        string    `json:"status"`
	Total         float64   `json:"total"`
	AmountDue     float64   `json:"amountDue"`
	AmountPaid    float64   `json:"amountPaid"`
	DueDate       *string   `json:"dueDate"`
	CreatedAt     time.Time `json:"createdAt"`
}

type LinkedContact struct {
	ID        string  `json:"id"`
	FirstName string  `json:"firstName"`
	LastName  *string `json:"lastName"`
	Email     *string `json:"email"`
	Phone     *string `json:"phone"`
	Title     *string `json:"title"`
}

type LinkedLead struct {
	ID        string    `json:"id"`
	FirstName string    `json:"firstName"`
	LastName  *string   `json:"lastName"`
	Email     *string   `json:"email"`
	Phone     *string   `json:"phone"`
	Title     *string   `json:"title"`
	Stage     string    `json:"stage"`
	Value     *float64  `json:"value"`
	CreatedAt time.Time `json:"createdAt"`
}

type FullCompanyProfilePayload struct {
	Account  Account         `json:"account"`
	Profile  CompanyProfile  `json:"profile"`
	Deals    []LinkedDeal    `json:"deals"`
	Quotes   []LinkedQuote   `json:"quotes"`
	Invoices []LinkedInvoice `json:"invoices"`
	Contacts []LinkedContact `json:"contacts"`
	Leads    []LinkedLead    `json:"leads"`
}

type ProfileInput struct {
	Name           string          `json:"name"`
	Website        *string         `json:"website"`
	Industry       *string         `json:"industry"`
	Phone          *string         `json:"phone"`
	Notes          *string         `json:"notes"`
	OwnerUserID    *string         `json:"ownerUserId"`
	Tagline        *string         `json:"tagline"`
	Description    *string         `json:"description"`
	PrimaryColor   *string         `json:"primaryColor"`
	BannerURL      *string         `json:"bannerUrl"`
	PlantLocations []PlantLocation `json:"plantLocations"`
	AIDetections   []string        `json:"aiDetections"`
	HardwareSpecs  HardwareSpecs   `json:"hardwareSpecs"`
	AMCStatus      *string         `json:"amcStatus"`
	AMCStartDate   *string         `json:"amcStartDate"`
	AMCEndDate     *string         `json:"amcEndDate"`
	AMCValue       *float64        `json:"amcValue"`
	CustomSections []CustomSection `json:"customSections"`
}

func (s *store) getFullProfile(ctx context.Context, orgID, companyID string) (FullCompanyProfilePayload, error) {
	acc, err := s.get(ctx, orgID, companyID)
	if err != nil {
		return FullCompanyProfilePayload{}, err
	}

	prof := CompanyProfile{
		AccountID:      companyID,
		PrimaryColor:   "#6366f1",
		PlantLocations: []PlantLocation{},
		AIDetections:   []string{},
		HardwareSpecs:  HardwareSpecs{},
		AMCStatus:      "none",
		CustomSections: []CustomSection{},
		CreatedAt:      acc.CreatedAt,
		UpdatedAt:      acc.UpdatedAt,
	}

	var plantRaw, hwRaw, csRaw []byte
	var amcStart, amcEnd *string
	var amcVal *float64

	row := s.pool.QueryRow(ctx,
		`SELECT account_id::text, tagline, description, primary_color, banner_url,
		        plant_locations, ai_detections, hardware_specs, amc_status,
		        amc_start_date::text, amc_end_date::text, amc_value, custom_sections,
		        created_at, updated_at
		 FROM account_profiles
		 WHERE account_id = $1`, companyID)

	var pID string
	err = row.Scan(
		&pID, &prof.Tagline, &prof.Description, &prof.PrimaryColor, &prof.BannerURL,
		&plantRaw, &prof.AIDetections, &hwRaw, &prof.AMCStatus,
		&amcStart, &amcEnd, &amcVal, &csRaw,
		&prof.CreatedAt, &prof.UpdatedAt,
	)
	if err == nil {
		prof.AMCStartDate = amcStart
		prof.AMCEndDate = amcEnd
		if amcVal != nil {
			prof.AMCValue = *amcVal
		}
		if len(plantRaw) > 0 {
			_ = jsonUnmarshal(plantRaw, &prof.PlantLocations)
		}
		if len(hwRaw) > 0 {
			_ = jsonUnmarshal(hwRaw, &prof.HardwareSpecs)
		}
		if len(csRaw) > 0 {
			_ = jsonUnmarshal(csRaw, &prof.CustomSections)
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return FullCompanyProfilePayload{}, translate(err)
	}

	if prof.PlantLocations == nil {
		prof.PlantLocations = []PlantLocation{}
	}
	if prof.AIDetections == nil {
		prof.AIDetections = []string{}
	}
	if prof.CustomSections == nil {
		prof.CustomSections = []CustomSection{}
	}

	// Fetch linked deals
	deals := make([]LinkedDeal, 0)
	dRows, err := s.pool.Query(ctx,
		`SELECT id::text, title, stage, amount::float8,
		        probability, NULL::text, NULL::text,
		        expected_close_date::text, notes, lead_id::text,
		        total_cameras, location, products, created_at
		 FROM deals
		 WHERE (account_id = $1 OR account_id IN (
		     SELECT id FROM accounts WHERE lower(trim(name)) = lower(trim($2)) AND deleted_at IS NULL
		 )) AND deleted_at IS NULL
		 ORDER BY created_at DESC`, companyID, acc.Name)
	if err == nil {
		defer dRows.Close()
		for dRows.Next() {
			var d LinkedDeal
			if scanErr := dRows.Scan(&d.ID, &d.Title, &d.Stage, &d.Amount, &d.Probability, &d.SiteAssessmentDate, &d.SiteAssessmentLoc, &d.ExpectedCloseDate, &d.Remark, &d.LeadID, &d.TotalCameras, &d.Location, &d.Products, &d.CreatedAt); scanErr == nil {
				deals = append(deals, d)
			}
		}
	}

	// Fetch linked quotes
	quotes := make([]LinkedQuote, 0)
	qRows, err := s.pool.Query(ctx,
		`SELECT q.id::text,
		        'Q-' || upper(substr(q.id::text, 1, 8)),
		        q.status,
		        COALESCE(v.total, 0)::float8,
		        COALESCE(v.currency, 'USD'),
		        COALESCE(v.version_number, 1),
		        q.valid_until::text,
		        q.created_at
		 FROM quotes q
		 LEFT JOIN quote_versions v ON v.quote_id = q.id AND v.is_current
		 WHERE (q.account_id = $1 OR q.account_id IN (
		     SELECT id FROM accounts WHERE lower(trim(name)) = lower(trim($2)) AND deleted_at IS NULL
		 )) AND q.deleted_at IS NULL
		 ORDER BY q.created_at DESC`, companyID, acc.Name)
	if err == nil {
		defer qRows.Close()
		for qRows.Next() {
			var q LinkedQuote
			if scanErr := qRows.Scan(&q.ID, &q.Number, &q.Status, &q.Total, &q.Currency, &q.CurrentVersion, &q.ValidUntil, &q.CreatedAt); scanErr == nil {
				quotes = append(quotes, q)
			}
		}
	}

	// Fetch linked invoices.
	//
	// The billed figure is amount_due: there is no `total` column on this table,
	// and selecting one made every row error and the whole tab render empty. The
	// paid figure comes from settled payments, the same derivation the invoices
	// module uses, so a part-paid invoice shows what is actually outstanding.
	invoices := make([]LinkedInvoice, 0)
	iRows, err := s.pool.Query(ctx,
		`SELECT i.id::text, i.invoice_number,
		        -- No title column on this table; the scan position is kept so the
		        -- JSON contract does not change.
		        NULL::text AS title,
		        i.status,
		        i.amount_due::float8,
		        (i.amount_due - paid.amt)::float8 AS amount_due,
		        paid.amt::float8                  AS amount_paid,
		        i.due_date::text,
		        i.created_at
		 FROM invoices i
		 CROSS JOIN LATERAL (
		   SELECT COALESCE(sum(amount), 0) AS amt
		     FROM payments pm
		    WHERE pm.invoice_id = i.id AND pm.status = 'succeeded'
		 ) paid
		 WHERE (i.account_id = $1 OR i.account_id IN (
		     SELECT id FROM accounts WHERE lower(trim(name)) = lower(trim($2)) AND deleted_at IS NULL
		 )) AND i.deleted_at IS NULL
		 ORDER BY i.created_at DESC`, companyID, acc.Name)
	if err != nil {
		// Previously swallowed. A tab that renders empty because its query is
		// broken looks identical to a client with no invoices, which is how this
		// went unnoticed: fail loudly instead.
		return FullCompanyProfilePayload{}, fmt.Errorf("linked invoices: %w", err)
	}
	defer iRows.Close()
	for iRows.Next() {
		var inv LinkedInvoice
		if scanErr := iRows.Scan(&inv.ID, &inv.InvoiceNumber, &inv.Title, &inv.Status, &inv.Total, &inv.AmountDue, &inv.AmountPaid, &inv.DueDate, &inv.CreatedAt); scanErr != nil {
			return FullCompanyProfilePayload{}, fmt.Errorf("scan linked invoice: %w", scanErr)
		}
		invoices = append(invoices, inv)
	}

	// Fetch linked contacts
	contacts := make([]LinkedContact, 0)
	cRows, err := s.pool.Query(ctx,
		`SELECT id::text, first_name, last_name, email, phone, title
		 FROM contacts
		 WHERE (account_id = $1 OR account_id IN (
		     SELECT id FROM accounts WHERE lower(trim(name)) = lower(trim($2)) AND deleted_at IS NULL
		 )) AND deleted_at IS NULL
		 ORDER BY created_at DESC`, companyID, acc.Name)
	if err == nil {
		defer cRows.Close()
		for cRows.Next() {
			var c LinkedContact
			if scanErr := cRows.Scan(&c.ID, &c.FirstName, &c.LastName, &c.Email, &c.Phone, &c.Title); scanErr == nil {
				contacts = append(contacts, c)
			}
		}
	}

	// Fetch linked leads
	leads := make([]LinkedLead, 0)
	lRows, err := s.pool.Query(ctx,
		`SELECT l.id::text,
		        COALESCE(l.contact_name, ''),
		        NULL::text AS last_name,
		        l.email, l.phone, l.job_title,
		        l.status, l.value_estimate,
		        l.created_at
		 FROM leads l
		 WHERE (l.account_id = $1 OR l.account_id IN (
		     SELECT id FROM accounts WHERE lower(trim(name)) = lower(trim($2)) AND deleted_at IS NULL
		 )) AND l.deleted_at IS NULL
		 ORDER BY l.created_at DESC`, companyID, acc.Name)
	if err == nil {
		defer lRows.Close()
		for lRows.Next() {
			var l LinkedLead
			if scanErr := lRows.Scan(&l.ID, &l.FirstName, &l.LastName, &l.Email, &l.Phone, &l.Title, &l.Stage, &l.Value, &l.CreatedAt); scanErr == nil {
				leads = append(leads, l)
			}
		}
	}

	acc.DealCount = len(deals)
	acc.ContactCount = len(contacts)
	acc.LeadCount = len(leads)

	return FullCompanyProfilePayload{
		Account:  acc,
		Profile:  prof,
		Deals:    deals,
		Quotes:   quotes,
		Invoices: invoices,
		Contacts: contacts,
		Leads:    leads,
	}, nil
}

func (s *store) upsertProfile(ctx context.Context, orgID, companyID string, in ProfileInput) (FullCompanyProfilePayload, error) {
	// Update main company details
	_, err := s.update(ctx, orgID, companyID, Input{
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

	plantRaw, _ := jsonMarshal(in.PlantLocations)
	hwRaw, _ := jsonMarshal(in.HardwareSpecs)
	csRaw, _ := jsonMarshal(in.CustomSections)

	primaryColor := "#6366f1"
	if in.PrimaryColor != nil && *in.PrimaryColor != "" {
		primaryColor = *in.PrimaryColor
	}

	amcStatus := "none"
	if in.AMCStatus != nil && *in.AMCStatus != "" {
		amcStatus = *in.AMCStatus
	}

	amcVal := float64(0)
	if in.AMCValue != nil {
		amcVal = *in.AMCValue
	}

	aiDetections := in.AIDetections
	if aiDetections == nil {
		aiDetections = []string{}
	}

	_, err = s.pool.Exec(ctx,
		`INSERT INTO account_profiles (
			account_id, tagline, description, primary_color, banner_url,
			plant_locations, ai_detections, hardware_specs, amc_status,
			amc_start_date, amc_end_date, amc_value, custom_sections, updated_at
		 ) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9,
			$10, $11, $12, $13, now()
		 )
		 ON CONFLICT (account_id) DO UPDATE SET
			tagline = EXCLUDED.tagline,
			description = EXCLUDED.description,
			primary_color = EXCLUDED.primary_color,
			banner_url = EXCLUDED.banner_url,
			plant_locations = EXCLUDED.plant_locations,
			ai_detections = EXCLUDED.ai_detections,
			hardware_specs = EXCLUDED.hardware_specs,
			amc_status = EXCLUDED.amc_status,
			amc_start_date = EXCLUDED.amc_start_date,
			amc_end_date = EXCLUDED.amc_end_date,
			amc_value = EXCLUDED.amc_value,
			custom_sections = EXCLUDED.custom_sections,
			updated_at = now()`,
		companyID, in.Tagline, in.Description, primaryColor, in.BannerURL,
		plantRaw, aiDetections, hwRaw, amcStatus,
		in.AMCStartDate, in.AMCEndDate, amcVal, csRaw,
	)
	if err != nil {
		return FullCompanyProfilePayload{}, translate(err)
	}

	return s.getFullProfile(ctx, orgID, companyID)
}
