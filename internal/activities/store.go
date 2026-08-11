// Package activities is the activity log — notes, calls and meetings a person
// records, plus system events the app writes when something changes state.
//
// It is the spine several other features read from: "stalled", "days idle",
// "last activity" and Company health are all questions about when something last
// happened, and this is the only table that can answer them.
package activities

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound means no activity with that id exists in the caller's org.
	ErrNotFound = errors.New("activity not found")
	// ErrRefNotFound means a referenced record isn't in the caller's org.
	ErrRefNotFound = errors.New("referenced record is not in this organization")
	// ErrSystemImmutable means someone tried to edit or delete a system event.
	ErrSystemImmutable = errors.New("system events cannot be changed")
)

const (
	pgInvalidTextRepr     = "22P02"
	pgForeignKeyViolation = "23503"
	pgCheckViolation      = "23514"
)

// Activity is one entry in the log.
type Activity struct {
	ID      string  `json:"id"`
	Kind    string  `json:"kind"`
	Subject *string `json:"subject"`
	Body    *string `json:"body"`
	// OccurredAt is when it happened, which is not always when it was typed in.
	OccurredAt      time.Time `json:"occurredAt"`
	DurationMinutes *int      `json:"durationMinutes"`

	LeadID    *string `json:"leadId"`
	DealID    *string `json:"dealId"`
	AccountID *string `json:"accountId"`
	ContactID *string `json:"contactId"`
	QuoteID   *string `json:"quoteId"`
	InvoiceID *string `json:"invoiceId"`

	// Denormalized labels so a timeline row needs no further lookups.
	LeadName    *string `json:"leadName"`
	DealTitle   *string `json:"dealTitle"`
	AccountName *string `json:"accountName"`
	ContactName *string `json:"contactName"`

	CreatedBy   *string   `json:"createdBy"`
	AuthorName  *string   `json:"authorName"`
	AuthorEmail *string   `json:"authorEmail"`
	CreatedAt   time.Time `json:"createdAt"`
}

type store struct {
	pool *pgxpool.Pool
}

const activityColumns = `
	a.id::text, a.kind, a.subject, a.body, a.occurred_at, a.duration_minutes,
	a.lead_id::text, a.deal_id::text, a.account_id::text, a.contact_id::text,
	a.quote_id::text, a.invoice_id::text,
	NULLIF(concat_ws(' ', l.first_name, l.last_name), ''),
	d.title, acc.name,
	NULLIF(concat_ws(' ', c.first_name, c.last_name), ''),
	a.created_by::text, u.name, u.email, a.created_at`

const activityFrom = `
	FROM activities a
	LEFT JOIN leads    l   ON l.id = a.lead_id
	LEFT JOIN deals    d   ON d.id = a.deal_id
	LEFT JOIN accounts acc ON acc.id = a.account_id
	LEFT JOIN contacts c   ON c.id = a.contact_id
	LEFT JOIN users    u   ON u.id = a.created_by `

// Filter narrows a timeline to one entity. All fields are optional; an empty
// filter returns the org's whole feed.
type Filter struct {
	LeadID    string
	DealID    string
	AccountID string
	ContactID string
	QuoteID   string
	InvoiceID string
	Limit     int
}

// list returns matching activities, newest first.
//
// Each clause is `($n = ” OR col = $n::uuid)` so one prepared statement serves
// every combination of filters rather than the code assembling SQL by hand.
func (s *store) list(ctx context.Context, orgID string, f Filter) ([]Activity, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+activityColumns+activityFrom+
			`WHERE a.org_id = $1
			   AND ($2 = '' OR a.lead_id    = $2::uuid)
			   AND ($3 = '' OR a.deal_id    = $3::uuid)
			   AND ($4 = '' OR a.account_id = $4::uuid)
			   AND ($5 = '' OR a.contact_id = $5::uuid)
			   AND ($6 = '' OR a.quote_id   = $6::uuid)
			   AND ($7 = '' OR a.invoice_id = $7::uuid)
			 ORDER BY a.occurred_at DESC, a.created_at DESC, a.id
			 LIMIT $8`,
		orgID, f.LeadID, f.DealID, f.AccountID, f.ContactID, f.QuoteID, f.InvoiceID, f.Limit)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	out := make([]Activity, 0, f.Limit)
	for rows.Next() {
		a, err := scanActivity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *store) get(ctx context.Context, orgID, id string) (Activity, error) {
	return scanActivity(s.pool.QueryRow(ctx,
		`SELECT `+activityColumns+activityFrom+`WHERE a.org_id = $1 AND a.id = $2`, orgID, id))
}

func (s *store) create(ctx context.Context, orgID, userID string, in Input) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`INSERT INTO activities
		   (org_id, kind, subject, body, occurred_at, duration_minutes,
		    lead_id, deal_id, account_id, contact_id, quote_id, invoice_id, created_by)
		 VALUES ($1, $2, $3, $4, COALESCE($5, now()), $6, $7, $8, $9, $10, $11, $12, $13)
		 RETURNING id::text`,
		orgID, in.Kind, in.Subject, in.Body, in.OccurredAt, in.DurationMinutes,
		in.LeadID, in.DealID, in.AccountID, in.ContactID, in.QuoteID, in.InvoiceID,
		nilIfEmpty(userID),
	).Scan(&id)
	return id, translate(err)
}

// update edits a human-logged entry. System events are excluded in SQL so the
// check can't be skipped by a caller that forgets it.
func (s *store) update(ctx context.Context, orgID, id string, in Input) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE activities
		 SET kind = $3, subject = $4, body = $5,
		     occurred_at = COALESCE($6, occurred_at), duration_minutes = $7
		 WHERE org_id = $1 AND id = $2 AND kind <> 'system'`,
		orgID, id, in.Kind, in.Subject, in.Body, in.OccurredAt, in.DurationMinutes)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return s.explainWriteMiss(ctx, orgID, id)
	}
	return nil
}

func (s *store) delete(ctx context.Context, orgID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM activities WHERE org_id = $1 AND id = $2 AND kind <> 'system'`, orgID, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return s.explainWriteMiss(ctx, orgID, id)
	}
	return nil
}

// explainWriteMiss tells "no such activity" apart from "that's a system event".
func (s *store) explainWriteMiss(ctx context.Context, orgID, id string) error {
	var kind string
	err := s.pool.QueryRow(ctx,
		`SELECT kind FROM activities WHERE org_id = $1 AND id = $2`, orgID, id).Scan(&kind)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isPgCode(err, pgInvalidTextRepr):
		return ErrNotFound
	case err != nil:
		return err
	case kind == "system":
		return ErrSystemImmutable
	default:
		return ErrNotFound
	}
}

func (s *store) refInOrg(ctx context.Context, table, orgID, id string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM `+table+` WHERE org_id = $1 AND id = $2)`,
		orgID, id).Scan(&exists)
	if err != nil {
		if isPgCode(err, pgInvalidTextRepr) {
			return false, nil
		}
		return false, err
	}
	return exists, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanActivity(row rowScanner) (Activity, error) {
	var a Activity
	err := row.Scan(
		&a.ID, &a.Kind, &a.Subject, &a.Body, &a.OccurredAt, &a.DurationMinutes,
		&a.LeadID, &a.DealID, &a.AccountID, &a.ContactID, &a.QuoteID, &a.InvoiceID,
		&a.LeadName, &a.DealTitle, &a.AccountName, &a.ContactName,
		&a.CreatedBy, &a.AuthorName, &a.AuthorEmail, &a.CreatedAt)
	if err != nil {
		return Activity{}, translate(err)
	}
	return a, nil
}

func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows), isPgCode(err, pgInvalidTextRepr):
		return ErrNotFound
	case isPgCode(err, pgForeignKeyViolation):
		return ErrRefNotFound
	case isPgCode(err, pgCheckViolation):
		return ErrNotFound
	default:
		return err
	}
}

func isPgCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
