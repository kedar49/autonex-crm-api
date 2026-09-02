package leads

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound means no lead with that id exists.
	ErrNotFound = errors.New("lead not found")
	// ErrRefNotFound means a referenced owner, contact or company doesn't exist.
	ErrRefNotFound = errors.New("referenced record is not in this organization")
)

const (
	pgInvalidTextRepr     = "22P02"
	pgCheckViolation      = "23514"
	pgForeignKeyViolation = "23503"
)

// Lead is the module's view of a row, plus the joined labels a list row needs.
type Lead struct {
	ID        string   `json:"id"`
	FirstName string   `json:"firstName"`
	LastName  *string  `json:"lastName"`
	Title     *string  `json:"title"`
	Email     *string  `json:"email"`
	Phone     *string  `json:"phone"`
	LinkedIn  *string  `json:"linkedinUrl"`
	Source    *string  `json:"source"`
	Notes     *string  `json:"notes"`
	Value     *float64 `json:"value"`
	Stage     string   `json:"stage"`

	// Company: account_id is the real link, `company` a free-text fallback for a
	// lead captured before anyone made the account record.
	AccountID       *string `json:"accountId"`
	AccountName     *string `json:"accountName"`
	AccountIndustry *string `json:"accountIndustry"`
	Company         *string `json:"company"`

	ContactID *string `json:"contactId"`

	OwnerUserID *string `json:"ownerUserId"`
	OwnerName   *string `json:"ownerName"`
	OwnerEmail  *string `json:"ownerEmail"`

	FollowUpAt *time.Time `json:"followUpAt"`
	// Derived, not stored: whether the follow-up is past due and the lead is
	// still in play.
	Overdue  bool `json:"overdue"`
	DueToday bool `json:"dueToday"`
	// LastContactedAt is the most recent thing a *person* logged — the brief's
	// "date of contact" column. System events don't count as contact.
	LastContactedAt *time.Time `json:"lastContactedAt"`

	ConvertedAt        *time.Time `json:"convertedAt"`
	ConvertedDealID    *string    `json:"convertedDealId"`
	ConvertedContactID *string    `json:"convertedContactId"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type store struct {
	pool *pgxpool.Pool
}

// This deployment's leads table stores a lead's identity and progress in
// contact_name / job_title / status / value_estimate / next_follow_up_date, and
// links a company rather than an account. leadColumns maps those onto the same
// 28 scan positions scanLead has always read, so the Go model, the JSON contract
// and the frontend stay untouched — the translation lives in SQL, in one place,
// instead of spreading through the module.
//
// Columns with no counterpart in this database (last name, the account link, the
// conversion trail) are selected as typed NULLs to hold their position.
const leadColumns = `
	l.id::text,
	COALESCE(l.contact_name, '')       AS first_name,
	NULL::text                         AS last_name,
	l.job_title                        AS title,
	l.email, l.phone, l.linkedin_url, l.source, l.notes,
	l.value_estimate                   AS value,
	l.status                           AS stage,
	l.company_id::text                 AS account_id,
	c.name                             AS account_name,
	c.industry                         AS account_industry,
	c.name                             AS company,
	l.contact_id::text,
	l.assigned_to::text                AS owner_user_id,
	p.full_name                        AS owner_name,
	NULL::text                         AS owner_email,
	l.next_follow_up_date::timestamptz AS follow_up_at,
	-- COALESCE is load-bearing, not defensive: a lead with no follow-up date
	-- compares NULL, not false, and pgx cannot scan that into a bool.
	COALESCE(l.next_follow_up_date < CURRENT_DATE
	   AND l.status NOT IN ('closed','not interested'), false),
	COALESCE(l.next_follow_up_date = CURRENT_DATE
	   AND l.status NOT IN ('closed','not interested'), false),
	-- Activities are polymorphic here (entity_type/entity_id), not a lead_id FK.
	(SELECT max(act.occurred_at) FROM activities act
	  WHERE act.entity_type = 'lead' AND act.entity_id = l.id
	    AND act.type <> 'system'),
	NULL::timestamptz                  AS converted_at,
	NULL::text                         AS converted_deal_id,
	NULL::text                         AS converted_contact_id,
	l.created_at, l.updated_at`

const leadFrom = `
	FROM leads l
	LEFT JOIN companies c ON c.id = l.company_id
	LEFT JOIN profiles  p ON p.id = l.assigned_to `

// urgencyOrder is the brief's "default sort by urgency": anything with a due
// date first (oldest, so overdue leads lead), then unscheduled work, then the
// leads that are already finished.
const urgencyOrder = `
	ORDER BY
	  CASE
	    WHEN l.status IN ('closed','not interested') THEN 2
	    WHEN l.next_follow_up_date IS NULL       THEN 1
	    ELSE 0
	  END,
	  l.next_follow_up_date ASC NULLS LAST,
	  l.created_at DESC, l.id`

// filterClause narrows the list. `overdue` and `due_today` are views over the
// follow-up date rather than stages, so they can't be compared to the status
// column; everything else is a plain status match.
//
// This deployment is single-tenant: lead rows carry no organization link, so
// there is no org predicate to apply. Rows soft-deleted through deleted_at are
// excluded here and in every other read below.
const filterClause = `
	WHERE l.deleted_at IS NULL
	  AND ($1 = '' OR
	       ($1 = 'overdue'   AND l.next_follow_up_date IS NOT NULL
	                         AND l.next_follow_up_date < CURRENT_DATE
	                         AND l.status NOT IN ('closed','not interested')) OR
	       ($1 = 'due_today' AND l.next_follow_up_date = CURRENT_DATE
	                         AND l.status NOT IN ('closed','not interested')) OR
	       ($1 = 'open'      AND l.status NOT IN ('closed','not interested')) OR
	       ($1 NOT IN ('overdue','due_today','open') AND l.status = $1))`

func (s *store) list(ctx context.Context, orgID, filter string, limit, offset int) ([]Lead, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+leadColumns+leadFrom+filterClause+urgencyOrder+`
		 LIMIT $2 OFFSET $3`, filter, limit, offset)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	out := make([]Lead, 0, limit)
	for rows.Next() {
		l, err := scanLead(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *store) count(ctx context.Context, orgID, filter string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM leads l `+filterClause, filter).Scan(&n)
	return n, err
}

// counts powers the funnel strip and the filter pills in one round-trip.
func (s *store) counts(ctx context.Context, orgID string) (map[string]int, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT status, count(*) FROM leads WHERE deleted_at IS NULL GROUP BY status
		 UNION ALL
		 SELECT 'overdue', count(*) FROM leads
		   WHERE deleted_at IS NULL AND next_follow_up_date IS NOT NULL
		     AND next_follow_up_date < CURRENT_DATE
		     AND status NOT IN ('closed','not interested')
		 UNION ALL
		 SELECT 'due_today', count(*) FROM leads
		   WHERE deleted_at IS NULL AND next_follow_up_date = CURRENT_DATE
		     AND status NOT IN ('closed','not interested')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]int, len(Stages)+2)
	for rows.Next() {
		var key string
		var n int
		if err := rows.Scan(&key, &n); err != nil {
			return nil, err
		}
		out[key] = n
	}
	return out, rows.Err()
}

func (s *store) get(ctx context.Context, orgID, id string) (Lead, error) {
	return scanLead(s.pool.QueryRow(ctx,
		`SELECT `+leadColumns+leadFrom+`WHERE l.id = $1 AND l.deleted_at IS NULL`, id))
}

// create writes the fields this database has a home for. The account link is
// company_id: an account is a company in this schema, so the accountId the client
// sends is stored there. Only the last name and the free-text company fallback
// have no column, and those are dropped.
func (s *store) create(ctx context.Context, orgID string, in Input) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`INSERT INTO leads
		   (contact_name, job_title, email, phone, linkedin_url, contact_id,
		    company_id, source, notes, value_estimate, status, assigned_to,
		    next_follow_up_date)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::date)
		 RETURNING id::text`,
		in.FirstName, in.Title, in.Email, in.Phone, in.LinkedIn, in.ContactID,
		in.AccountID, in.Source, in.Notes, in.Value, in.Stage, in.OwnerUserID,
		in.FollowUpAt,
	).Scan(&id)
	return id, translate(err)
}

func (s *store) update(ctx context.Context, orgID, id string, in Input) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE leads
		 SET contact_name = $2, job_title = $3, email = $4, phone = $5,
		     linkedin_url = $6, contact_id = $7, company_id = $8, source = $9,
		     notes = $10, value_estimate = $11, status = $12, assigned_to = $13,
		     next_follow_up_date = $14::date, updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`,
		id, in.FirstName, in.Title, in.Email, in.Phone, in.LinkedIn,
		in.ContactID, in.AccountID, in.Source, in.Notes, in.Value, in.Stage,
		in.OwnerUserID, in.FollowUpAt)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// advance moves a lead along the lifecycle and optionally reschedules the next
// touch, in one statement — the two always change together in the UI, and doing
// them separately would leave a window where a lead is "call booked" with no call
// in the diary.
//
// `clearFollowUp` distinguishes "leave it alone" (nil date, false) from "there is
// nothing more to chase" (nil date, true), which a nullable field alone cannot.
func (s *store) advance(
	ctx context.Context, orgID, id, stage string, followUp *time.Time, clearFollowUp bool,
) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE leads
		 SET status = $2,
		     next_follow_up_date = CASE
		       WHEN $4 THEN NULL
		       WHEN $3::date IS NOT NULL THEN $3::date
		       ELSE next_follow_up_date
		     END,
		     updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`,
		id, stage, followUp, clearFollowUp)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return s.explainWriteMiss(ctx, orgID, id)
	}
	return nil
}

func (s *store) delete(ctx context.Context, orgID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM leads WHERE id = $1`, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// explainWriteMiss reports why an update matched no row. This schema keeps no
// conversion trail, so a miss can only mean the lead is absent or soft-deleted.
func (s *store) explainWriteMiss(ctx context.Context, orgID, id string) error {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM leads WHERE id = $1)`, id).Scan(&exists)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isPgCode(err, pgInvalidTextRepr):
		return ErrNotFound
	case err != nil:
		return err
	default:
		return ErrNotFound
	}
}

// refInOrg checks a client-supplied foreign key. Single-tenant here, so
// existence is the only thing left to verify.
func (s *store) refInOrg(ctx context.Context, table, orgID, id string) (bool, error) {
	if table == "accounts" {
		table = "companies"
	}
	if table == "users" {
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

// Stats backs the dashboard: per-stage counts and value in one pass.
type Stats struct {
	Stage string  `json:"stage"`
	Count int     `json:"count"`
	Value float64 `json:"value"`
}

func (s *store) stats(ctx context.Context, orgID string) ([]Stats, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT status, count(*), COALESCE(sum(value_estimate), 0)::float8
		 FROM leads WHERE deleted_at IS NULL GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Stats, 0, len(Stages))
	for rows.Next() {
		var st Stats
		if err := rows.Scan(&st.Stage, &st.Count, &st.Value); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanLead(row rowScanner) (Lead, error) {
	var l Lead
	err := row.Scan(
		&l.ID, &l.FirstName, &l.LastName, &l.Title, &l.Email, &l.Phone,
		&l.LinkedIn, &l.Source, &l.Notes, &l.Value, &l.Stage,
		&l.AccountID, &l.AccountName, &l.AccountIndustry, &l.Company,
		&l.ContactID,
		&l.OwnerUserID, &l.OwnerName, &l.OwnerEmail,
		&l.FollowUpAt, &l.Overdue, &l.DueToday, &l.LastContactedAt,
		&l.ConvertedAt, &l.ConvertedDealID, &l.ConvertedContactID,
		&l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		return Lead{}, translate(err)
	}
	return l, nil
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
		// Only the status CHECK can fire; the service validates stages first.
		return ErrNotFound
	default:
		return err
	}
}

func isPgCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
