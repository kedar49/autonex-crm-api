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
	// ErrNotFound means no lead with that id exists in the caller's org. A lead
	// belonging to another tenant is indistinguishable from a missing one.
	ErrNotFound = errors.New("lead not found")
	// ErrRefNotFound means a referenced owner, contact or company isn't in the org.
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

const leadColumns = `
	l.id::text, l.first_name, l.last_name, l.title, l.email, l.phone,
	l.linkedin_url, l.source, l.notes, l.value, l.stage,
	l.account_id::text, a.name, a.industry, l.company,
	l.contact_id::text,
	l.owner_user_id::text, u.name, u.email,
	l.follow_up_at,
	-- COALESCE is load-bearing, not defensive: a lead with no follow-up date
	-- compares NULL, not false, and pgx cannot scan that into a bool.
	COALESCE(l.follow_up_at < CURRENT_DATE
	   AND l.stage NOT IN ('converted','dropped'), false),
	COALESCE(l.follow_up_at = CURRENT_DATE
	   AND l.stage NOT IN ('converted','dropped'), false),
	(SELECT max(act.occurred_at) FROM activities act
	  WHERE act.lead_id = l.id AND act.kind <> 'system'),
	l.converted_at, l.converted_deal_id::text, l.converted_contact_id::text,
	l.created_at, l.updated_at`

const leadFrom = `
	FROM leads l
	LEFT JOIN accounts a ON a.id = l.account_id
	LEFT JOIN users    u ON u.id = l.owner_user_id `

// urgencyOrder is the brief's "default sort by urgency": anything with a due
// date first (oldest, so overdue leads lead), then unscheduled work, then the
// leads that are already finished.
const urgencyOrder = `
	ORDER BY
	  CASE
	    WHEN l.stage IN ('converted','dropped') THEN 2
	    WHEN l.follow_up_at IS NULL             THEN 1
	    ELSE 0
	  END,
	  l.follow_up_at ASC NULLS LAST,
	  l.created_at DESC, l.id`

// filterClause narrows the list. `overdue` and `due_today` are views over the
// follow-up date rather than stages, so they can't be compared to the stage
// column; everything else is a plain stage match.
const filterClause = `
	WHERE l.org_id = $1
	  AND ($2 = '' OR
	       ($2 = 'overdue'   AND l.follow_up_at IS NOT NULL
	                         AND l.follow_up_at < CURRENT_DATE
	                         AND l.stage NOT IN ('converted','dropped')) OR
	       ($2 = 'due_today' AND l.follow_up_at = CURRENT_DATE
	                         AND l.stage NOT IN ('converted','dropped')) OR
	       ($2 = 'open'      AND l.stage NOT IN ('converted','dropped')) OR
	       ($2 NOT IN ('overdue','due_today','open') AND l.stage = $2))`

func (s *store) list(ctx context.Context, orgID, filter string, limit, offset int) ([]Lead, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+leadColumns+leadFrom+filterClause+urgencyOrder+`
		 LIMIT $3 OFFSET $4`, orgID, filter, limit, offset)
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
		`SELECT count(*) FROM leads l `+filterClause, orgID, filter).Scan(&n)
	return n, err
}

// counts powers the funnel strip and the filter pills in one round-trip.
func (s *store) counts(ctx context.Context, orgID string) (map[string]int, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT stage, count(*) FROM leads WHERE org_id = $1 GROUP BY stage
		 UNION ALL
		 SELECT 'overdue', count(*) FROM leads
		   WHERE org_id = $1 AND follow_up_at IS NOT NULL AND follow_up_at < CURRENT_DATE
		     AND stage NOT IN ('converted','dropped')
		 UNION ALL
		 SELECT 'due_today', count(*) FROM leads
		   WHERE org_id = $1 AND follow_up_at = CURRENT_DATE
		     AND stage NOT IN ('converted','dropped')`, orgID)
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
		`SELECT `+leadColumns+leadFrom+`WHERE l.org_id = $1 AND l.id = $2`, orgID, id))
}

func (s *store) create(ctx context.Context, orgID string, in Input) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`INSERT INTO leads
		   (org_id, first_name, last_name, title, email, phone, linkedin_url,
		    company, account_id, contact_id, source, notes, value, stage,
		    owner_user_id, follow_up_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		 RETURNING id::text`,
		orgID, in.FirstName, in.LastName, in.Title, in.Email, in.Phone, in.LinkedIn,
		in.Company, in.AccountID, in.ContactID, in.Source, in.Notes, in.Value, in.Stage,
		in.OwnerUserID, in.FollowUpAt,
	).Scan(&id)
	return id, translate(err)
}

func (s *store) update(ctx context.Context, orgID, id string, in Input) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE leads
		 SET first_name = $3, last_name = $4, title = $5, email = $6, phone = $7,
		     linkedin_url = $8, company = $9, account_id = $10, contact_id = $11,
		     source = $12, notes = $13, value = $14, stage = $15,
		     owner_user_id = $16, follow_up_at = $17, updated_at = now()
		 WHERE org_id = $1 AND id = $2`,
		orgID, id, in.FirstName, in.LastName, in.Title, in.Email, in.Phone,
		in.LinkedIn, in.Company, in.AccountID, in.ContactID, in.Source, in.Notes,
		in.Value, in.Stage, in.OwnerUserID, in.FollowUpAt)
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
		 SET stage = $3,
		     follow_up_at = CASE
		       WHEN $5 THEN NULL
		       WHEN $4::date IS NOT NULL THEN $4::date
		       ELSE follow_up_at
		     END,
		     updated_at = now()
		 WHERE org_id = $1 AND id = $2 AND converted_at IS NULL`,
		orgID, id, stage, followUp, clearFollowUp)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return s.explainWriteMiss(ctx, orgID, id)
	}
	return nil
}

func (s *store) delete(ctx context.Context, orgID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM leads WHERE org_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// explainWriteMiss tells "no such lead" apart from "already converted".
func (s *store) explainWriteMiss(ctx context.Context, orgID, id string) error {
	var convertedAt *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT converted_at FROM leads WHERE org_id = $1 AND id = $2`, orgID, id).Scan(&convertedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isPgCode(err, pgInvalidTextRepr):
		return ErrNotFound
	case err != nil:
		return err
	case convertedAt != nil:
		return ErrAlreadyConverted
	default:
		return ErrNotFound
	}
}

// refInOrg checks a client-supplied foreign key against the caller's org.
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

// Stats backs the dashboard: per-stage counts and value in one pass.
type Stats struct {
	Stage string  `json:"stage"`
	Count int     `json:"count"`
	Value float64 `json:"value"`
}

func (s *store) stats(ctx context.Context, orgID string) ([]Stats, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT stage, count(*), COALESCE(sum(value), 0)::float8
		 FROM leads WHERE org_id = $1 GROUP BY stage`, orgID)
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
		// Only the stage CHECK can fire; the service validates stages first.
		return ErrNotFound
	default:
		return err
	}
}

func isPgCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
