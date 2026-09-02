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

// This deployment stores an activity's subject as a single (entity_type,
// entity_id) pair rather than one nullable FK column per entity type. The CASE
// expressions below fan that pair back out into the six id positions
// scanActivity reads, so the Go model and the JSON contract are unchanged.
//
// There is no subject or duration_minutes column here; both are typed NULLs.
const activityColumns = `
	a.id::text,
	a.type            AS kind,
	NULL::text        AS subject,
	a.body,
	a.occurred_at,
	NULL::int         AS duration_minutes,
	CASE WHEN a.entity_type = 'lead'    THEN a.entity_id::text END,
	CASE WHEN a.entity_type = 'deal'    THEN a.entity_id::text END,
	CASE WHEN a.entity_type = 'company' THEN a.entity_id::text END,
	CASE WHEN a.entity_type = 'contact' THEN a.entity_id::text END,
	CASE WHEN a.entity_type = 'quote'   THEN a.entity_id::text END,
	CASE WHEN a.entity_type = 'invoice' THEN a.entity_id::text END,
	l.contact_name,
	d.title, acc.name,
	NULLIF(concat_ws(' ', c.first_name, c.last_name), ''),
	a.author_id::text AS created_by,
	p.full_name       AS author_name,
	NULL::text        AS author_email,
	a.created_at`

// Each label join is gated on entity_type as well as the id, so a lead and a
// deal that happen to share a uuid cannot cross-populate each other's label.
const activityFrom = `
	FROM activities a
	LEFT JOIN leads     l   ON a.entity_type = 'lead'    AND l.id   = a.entity_id
	LEFT JOIN deals     d   ON a.entity_type = 'deal'    AND d.id   = a.entity_id
	LEFT JOIN companies acc ON a.entity_type = 'company' AND acc.id = a.entity_id
	LEFT JOIN contacts  c   ON a.entity_type = 'contact' AND c.id   = a.entity_id
	LEFT JOIN profiles  p   ON p.id = a.author_id `

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
			`WHERE ($1 = '' OR (a.entity_type = 'lead'    AND a.entity_id = $1::uuid))
			   AND ($2 = '' OR (a.entity_type = 'deal'    AND a.entity_id = $2::uuid))
			   AND ($3 = '' OR (a.entity_type = 'company' AND a.entity_id = $3::uuid))
			   AND ($4 = '' OR (a.entity_type = 'contact' AND a.entity_id = $4::uuid))
			   AND ($5 = '' OR (a.entity_type = 'quote'   AND a.entity_id = $5::uuid))
			   AND ($6 = '' OR (a.entity_type = 'invoice' AND a.entity_id = $6::uuid))
			 ORDER BY a.occurred_at DESC, a.created_at DESC, a.id
			 LIMIT $7`,
		f.LeadID, f.DealID, f.AccountID, f.ContactID, f.QuoteID, f.InvoiceID, f.Limit)
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
		`SELECT `+activityColumns+activityFrom+`WHERE a.id = $1`, id))
}

func (s *store) create(ctx context.Context, orgID, userID string, in Input) (string, error) {
	entityType, entityID := subjectOf(in)
	if entityType == "" {
		return "", ErrRefNotFound
	}

	// Subject and duration have no column in this schema and are dropped. The
	// author is a profile, so a caller with no matching profile records none
	// rather than failing the insert on the foreign key.
	var id string
	err := s.pool.QueryRow(ctx,
		`INSERT INTO activities
		   (entity_type, entity_id, type, body, occurred_at, author_id)
		 VALUES ($1, $2::uuid, $3, $4, COALESCE($5, now()),
		         (SELECT id FROM profiles WHERE id = $6::uuid))
		 RETURNING id::text`,
		entityType, entityID, in.Kind, in.Body, in.OccurredAt, nilIfEmpty(userID),
	).Scan(&id)
	return id, translate(err)
}

// update edits a human-logged entry. System events are excluded in SQL so the
// check can't be skipped by a caller that forgets it.
func (s *store) update(ctx context.Context, orgID, id string, in Input) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE activities
		 SET type = $2, body = $3,
		     occurred_at = COALESCE($4, occurred_at), updated_at = now()
		 WHERE id = $1 AND type <> 'system'`,
		id, in.Kind, in.Body, in.OccurredAt)
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
		`DELETE FROM activities WHERE id = $1 AND type <> 'system'`, id)
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
		`SELECT type FROM activities WHERE id = $1`, id).Scan(&kind)
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
	if table == "accounts" {
		table = "companies"
	}
	if table == "users" || table == "profiles" {
		var exists bool
		err := s.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM users WHERE id = $1) OR EXISTS (SELECT 1 FROM profiles WHERE id = $1)`, id).Scan(&exists)
		if err != nil {
			if isPgCode(err, pgInvalidTextRepr) {
				return false, nil
			}
			return false, err
		}
		return exists, nil
	}
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM `+table+` WHERE id = $1)`, id).Scan(&exists)
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

// subjectOf reduces the six optional entity ids on an Input to the single
// (entity_type, entity_id) pair this schema stores. The service rejects an input
// naming more than one, so first match wins.
func subjectOf(in Input) (string, string) {
	switch {
	case in.LeadID != nil && *in.LeadID != "":
		return "lead", *in.LeadID
	case in.DealID != nil && *in.DealID != "":
		return "deal", *in.DealID
	case in.AccountID != nil && *in.AccountID != "":
		return "company", *in.AccountID
	case in.ContactID != nil && *in.ContactID != "":
		return "contact", *in.ContactID
	case in.QuoteID != nil && *in.QuoteID != "":
		return "quote", *in.QuoteID
	case in.InvoiceID != nil && *in.InvoiceID != "":
		return "invoice", *in.InvoiceID
	}
	return "", ""
}
