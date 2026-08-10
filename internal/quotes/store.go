// Package quotes is the sales-quote domain module: a document made of line items
// whose money is computed by the database, never by a client.
package quotes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound means no quote with that id exists in the caller's org.
	ErrNotFound = errors.New("quote not found")
	// ErrRefNotFound means a referenced account/contact/deal/owner isn't in the org.
	ErrRefNotFound = errors.New("referenced record is not in this organization")
	// ErrNotDraft means the document has been issued and is no longer editable.
	ErrNotDraft = errors.New("only a draft quote can be changed")
)

const (
	pgInvalidTextRepr     = "22P02"
	pgCheckViolation      = "23514"
	pgForeignKeyViolation = "23503"
)

// Item is one line of a quote. Every money field is derived except the inputs.
type Item struct {
	ID              string  `json:"id"`
	Position        int     `json:"position"`
	Description     string  `json:"description"`
	Quantity        float64 `json:"quantity"`
	UnitPrice       float64 `json:"unitPrice"`
	DiscountPercent float64 `json:"discountPercent"`
	TaxPercent      float64 `json:"taxPercent"`
	// LineTotal is net of discount, before tax. Computed by the database.
	LineTotal float64 `json:"lineTotal"`
}

// Quote is the document. Totals are read-only to clients.
type Quote struct {
	ID       string  `json:"id"`
	Number   string  `json:"number"`
	Title    *string `json:"title"`
	Status   string  `json:"status"`
	Currency string  `json:"currency"`

	AccountID   *string `json:"accountId"`
	AccountName *string `json:"accountName"`
	ContactID   *string `json:"contactId"`
	ContactName *string `json:"contactName"`
	DealID      *string `json:"dealId"`
	DealTitle   *string `json:"dealTitle"`
	OwnerUserID *string `json:"ownerUserId"`
	OwnerName   *string `json:"ownerName"`
	OwnerEmail  *string `json:"ownerEmail"`

	Notes      *string    `json:"notes"`
	ValidUntil *time.Time `json:"validUntil"`

	Subtotal      float64 `json:"subtotal"`
	DiscountTotal float64 `json:"discountTotal"`
	TaxTotal      float64 `json:"taxTotal"`
	Total         float64 `json:"total"`

	SentAt     *time.Time `json:"sentAt"`
	AcceptedAt *time.Time `json:"acceptedAt"`
	DeclinedAt *time.Time `json:"declinedAt"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`

	// Populated by Get, omitted from list responses.
	Items []Item `json:"items,omitempty"`
	// ItemCount lets the list show "3 items" without fetching them.
	ItemCount int `json:"itemCount"`
}

type store struct {
	pool *pgxpool.Pool
}

const quoteColumns = `
	q.id::text, q.number, q.title, q.status, q.currency,
	q.account_id::text, a.name, q.contact_id::text,
	NULLIF(concat_ws(' ', c.first_name, c.last_name), ''),
	q.deal_id::text, d.title, q.owner_user_id::text, u.name, u.email,
	q.notes, q.valid_until,
	q.subtotal::float8, q.discount_total::float8, q.tax_total::float8, q.total::float8,
	q.sent_at, q.accepted_at, q.declined_at, q.created_at, q.updated_at,
	(SELECT count(*) FROM quote_items i WHERE i.quote_id = q.id)`

const quoteFrom = `
	FROM quotes q
	LEFT JOIN accounts a ON a.id = q.account_id
	LEFT JOIN contacts c ON c.id = q.contact_id
	LEFT JOIN deals    d ON d.id = q.deal_id
	LEFT JOIN users    u ON u.id = q.owner_user_id `

func (s *store) list(ctx context.Context, orgID, status string, limit, offset int) ([]Quote, error) {
	// A single statement with an optional filter: passing '' means "any status",
	// which keeps one query plan instead of two code paths.
	rows, err := s.pool.Query(ctx,
		`SELECT `+quoteColumns+quoteFrom+
			`WHERE q.org_id = $1 AND ($2 = '' OR q.status = $2)
			 ORDER BY q.created_at DESC, q.id
			 LIMIT $3 OFFSET $4`, orgID, status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Quote, 0, limit)
	for rows.Next() {
		q, err := scanQuote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (s *store) count(ctx context.Context, orgID, status string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM quotes WHERE org_id = $1 AND ($2 = '' OR status = $2)`,
		orgID, status).Scan(&n)
	return n, err
}

func (s *store) get(ctx context.Context, orgID, id string) (Quote, error) {
	q, err := scanQuote(s.pool.QueryRow(ctx,
		`SELECT `+quoteColumns+quoteFrom+`WHERE q.org_id = $1 AND q.id = $2`, orgID, id))
	if err != nil {
		return Quote{}, err
	}

	items, err := s.items(ctx, id)
	if err != nil {
		return Quote{}, err
	}
	q.Items = items
	return q, nil
}

func (s *store) items(ctx context.Context, quoteID string) ([]Item, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id::text, position, description, quantity::float8, unit_price::float8,
		        discount_percent::float8, tax_percent::float8, line_total::float8
		 FROM quote_items WHERE quote_id = $1 ORDER BY position, id`, quoteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Item, 0, 8)
	for rows.Next() {
		var i Item
		if err := rows.Scan(&i.ID, &i.Position, &i.Description, &i.Quantity, &i.UnitPrice,
			&i.DiscountPercent, &i.TaxPercent, &i.LineTotal); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// create allocates the next document number and writes the quote and its items.
func (s *store) create(ctx context.Context, orgID, currency string, in Input) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Row lock on the organization for the duration of the transaction, so two
	// concurrent creates can't take the same number.
	var seq int
	if err := tx.QueryRow(ctx,
		`UPDATE organizations SET quote_seq = quote_seq + 1 WHERE id = $1 RETURNING quote_seq`,
		orgID).Scan(&seq); err != nil {
		return "", err
	}
	number := fmt.Sprintf("Q-%04d", seq)

	var id string
	if err := tx.QueryRow(ctx,
		`INSERT INTO quotes
		   (org_id, number, title, currency, account_id, contact_id, deal_id,
		    owner_user_id, notes, valid_until)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 RETURNING id::text`,
		orgID, number, in.Title, currency, in.AccountID, in.ContactID, in.DealID,
		in.OwnerUserID, in.Notes, in.ValidUntil,
	).Scan(&id); err != nil {
		return "", translate(err)
	}

	if err := replaceItems(ctx, tx, orgID, id, in.Items); err != nil {
		return "", err
	}
	if err := recalculate(ctx, tx, orgID, id); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}

// update replaces the header and the whole item set, but only while draft.
func (s *store) update(ctx context.Context, orgID, id string, in Input) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Claim the row and assert it is still a draft in one statement, so the check
	// can't race a concurrent "send".
	var status string
	err = tx.QueryRow(ctx,
		`UPDATE quotes
		 SET title = $3, account_id = $4, contact_id = $5, deal_id = $6,
		     owner_user_id = $7, notes = $8, valid_until = $9, updated_at = now()
		 WHERE org_id = $1 AND id = $2 AND status = 'draft'
		 RETURNING status`,
		orgID, id, in.Title, in.AccountID, in.ContactID, in.DealID,
		in.OwnerUserID, in.Notes, in.ValidUntil).Scan(&status)

	if errors.Is(err, pgx.ErrNoRows) || isPgCode(err, pgInvalidTextRepr) {
		return s.explainWriteMiss(ctx, orgID, id)
	}
	if err != nil {
		return translate(err)
	}

	if err := replaceItems(ctx, tx, orgID, id, in.Items); err != nil {
		return err
	}
	if err := recalculate(ctx, tx, orgID, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// replaceItems swaps the whole item set.
//
// Delete-then-insert rather than diffing: item ids are not referenced by anything
// (nothing links to a quote line), the sets are small, and a diff would be more
// code for no observable difference.
func replaceItems(ctx context.Context, tx pgx.Tx, orgID, quoteID string, items []ItemInput) error {
	if _, err := tx.Exec(ctx, `DELETE FROM quote_items WHERE quote_id = $1`, quoteID); err != nil {
		return err
	}

	for i, item := range items {
		// line_total is computed here, in NUMERIC arithmetic: Postgres does exact
		// decimal maths, so the stored figure can't drift the way float64 would.
		if _, err := tx.Exec(ctx,
			`INSERT INTO quote_items
			   (quote_id, org_id, position, description, quantity, unit_price,
			    discount_percent, tax_percent, line_total)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8,
			         round($5::numeric * $6::numeric * (1 - $7::numeric / 100), 2))`,
			quoteID, orgID, i, item.Description, item.Quantity, item.UnitPrice,
			item.DiscountPercent, item.TaxPercent); err != nil {
			return translate(err)
		}
	}
	return nil
}

// recalculate re-derives the quote's totals from its items.
//
// Totals are never accepted from a client — a quote whose stated total disagreed
// with its lines would be a document you cannot defend.
func recalculate(ctx context.Context, tx pgx.Tx, orgID, quoteID string) error {
	_, err := tx.Exec(ctx,
		`WITH t AS (
		   SELECT
		     COALESCE(sum(round(quantity * unit_price, 2)), 0) AS gross,
		     COALESCE(sum(line_total), 0)                      AS net,
		     COALESCE(sum(round(line_total * tax_percent / 100, 2)), 0) AS tax
		   FROM quote_items WHERE quote_id = $2
		 )
		 UPDATE quotes
		 SET subtotal = t.gross,
		     discount_total = t.gross - t.net,
		     tax_total = t.tax,
		     total = t.net + t.tax,
		     updated_at = now()
		 FROM t
		 WHERE quotes.org_id = $1 AND quotes.id = $2`, orgID, quoteID)
	return err
}

// setStatus applies a lifecycle transition and stamps the matching timestamp.
func (s *store) setStatus(ctx context.Context, orgID, id, from, to string) error {
	// `status = $3` makes the transition atomic against a concurrent change: the
	// caller's view of the current status has to still be true.
	tag, err := s.pool.Exec(ctx,
		`UPDATE quotes
		 SET status = $4,
		     sent_at     = CASE WHEN $4 = 'sent'     THEN now() ELSE sent_at     END,
		     accepted_at = CASE WHEN $4 = 'accepted' THEN now() ELSE accepted_at END,
		     declined_at = CASE WHEN $4 = 'declined' THEN now() ELSE declined_at END,
		     updated_at  = now()
		 WHERE org_id = $1 AND id = $2 AND status = $3`,
		orgID, id, from, to)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// currentStatus reads the status for a transition check.
func (s *store) currentStatus(ctx context.Context, orgID, id string) (string, error) {
	var status string
	err := s.pool.QueryRow(ctx,
		`SELECT status FROM quotes WHERE org_id = $1 AND id = $2`, orgID, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) || isPgCode(err, pgInvalidTextRepr) {
		return "", ErrNotFound
	}
	return status, err
}

// delete removes a draft. An issued document is a record of what was sent, so
// deleting one is refused rather than quietly rewriting history.
func (s *store) delete(ctx context.Context, orgID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM quotes WHERE org_id = $1 AND id = $2 AND status = 'draft'`, orgID, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return s.explainWriteMiss(ctx, orgID, id)
	}
	return nil
}

// explainWriteMiss tells "no such quote" apart from "not a draft". Runs only on
// the failure path, so the happy path stays one statement.
func (s *store) explainWriteMiss(ctx context.Context, orgID, id string) error {
	status, err := s.currentStatus(ctx, orgID, id)
	if err != nil {
		return err
	}
	if status != "draft" {
		return ErrNotDraft
	}
	return ErrNotFound
}

// refInOrg checks a client-supplied foreign key against the caller's org.
func (s *store) refInOrg(ctx context.Context, table, orgID, id string) (bool, error) {
	// table is never user input — callers pass a literal.
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

// Stats backs the dashboard: per-status counts and value.
type Stats struct {
	Status string  `json:"status"`
	Count  int     `json:"count"`
	Value  float64 `json:"value"`
}

func (s *store) stats(ctx context.Context, orgID string) ([]Stats, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT status, count(*), COALESCE(sum(total), 0)::float8
		 FROM quotes WHERE org_id = $1 GROUP BY status`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Stats, 0, len(Statuses))
	for rows.Next() {
		var st Stats
		if err := rows.Scan(&st.Status, &st.Count, &st.Value); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanQuote(row rowScanner) (Quote, error) {
	var q Quote
	err := row.Scan(
		&q.ID, &q.Number, &q.Title, &q.Status, &q.Currency,
		&q.AccountID, &q.AccountName, &q.ContactID, &q.ContactName,
		&q.DealID, &q.DealTitle, &q.OwnerUserID, &q.OwnerName, &q.OwnerEmail,
		&q.Notes, &q.ValidUntil,
		&q.Subtotal, &q.DiscountTotal, &q.TaxTotal, &q.Total,
		&q.SentAt, &q.AcceptedAt, &q.DeclinedAt, &q.CreatedAt, &q.UpdatedAt,
		&q.ItemCount)
	if err != nil {
		return Quote{}, translate(err)
	}
	return q, nil
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
		// The status and percentage CHECKs; the service validates first, so this
		// is the belt to that braces.
		return ErrNotFound
	default:
		return err
	}
}

func isPgCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
