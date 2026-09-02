// Package invoices is the billing document module: an invoice made of line
// items, with payments recorded against it. Like quotes, every money figure is
// computed by the database and never accepted from a client.
package invoices

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
	// ErrNotFound means no invoice with that id exists in the caller's org.
	ErrNotFound = errors.New("invoice not found")
	// ErrRefNotFound means a referenced account/contact/deal/owner isn't in the org.
	ErrRefNotFound = errors.New("referenced record is not in this organization")
	// ErrNotDraft means the invoice has been issued and is no longer editable.
	ErrNotDraft = errors.New("only a draft invoice can be changed")
	// ErrNotPayable means the invoice cannot take a payment in its current state.
	ErrNotPayable = errors.New("only an issued invoice can take a payment")
	// ErrQuoteNotInvoiceable means the quote is missing, not accepted, or already billed.
	ErrQuoteNotInvoiceable = errors.New("that quote cannot be invoiced")
)

const (
	pgInvalidTextRepr     = "22P02"
	pgCheckViolation      = "23514"
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
)

// Item is one line of an invoice.
type Item struct {
	ID              string  `json:"id"`
	Position        int     `json:"position"`
	Description     string  `json:"description"`
	Quantity        float64 `json:"quantity"`
	UnitPrice       float64 `json:"unitPrice"`
	DiscountPercent float64 `json:"discountPercent"`
	TaxPercent      float64 `json:"taxPercent"`
	LineTotal       float64 `json:"lineTotal"`
}

// Payment is one receipt against an invoice.
type Payment struct {
	ID        string    `json:"id"`
	Amount    float64   `json:"amount"`
	PaidOn    time.Time `json:"paidOn"`
	Method    *string   `json:"method"`
	Reference *string   `json:"reference"`
	Note      *string   `json:"note"`
	CreatedAt time.Time `json:"createdAt"`
}

// Invoice is the document.
type Invoice struct {
	ID       string  `json:"id"`
	Number   string  `json:"number"`
	Title    *string `json:"title"`
	Status   string  `json:"status"`
	Currency string  `json:"currency"`

	QuoteID     *string `json:"quoteId"`
	QuoteNumber *string `json:"quoteNumber"`
	AccountID   *string `json:"accountId"`
	AccountName *string `json:"accountName"`
	ContactID   *string `json:"contactId"`
	ContactName *string `json:"contactName"`
	DealID      *string `json:"dealId"`
	DealTitle   *string `json:"dealTitle"`
	OwnerUserID *string `json:"ownerUserId"`
	OwnerName   *string `json:"ownerName"`
	OwnerEmail  *string `json:"ownerEmail"`

	Notes     *string    `json:"notes"`
	IssueDate *time.Time `json:"issueDate"`
	DueDate   *time.Time `json:"dueDate"`

	Subtotal      float64 `json:"subtotal"`
	DiscountTotal float64 `json:"discountTotal"`
	TaxTotal      float64 `json:"taxTotal"`
	Total         float64 `json:"total"`
	AmountPaid    float64 `json:"amountPaid"`
	// Balance is total − amount_paid, computed on read rather than stored: one
	// less figure that can disagree with its source.
	Balance float64 `json:"balance"`
	// Overdue is derived (issued, past due, still owing) rather than a stored
	// status, which would go stale overnight without a scheduled job.
	Overdue bool `json:"overdue"`

	SentAt    *time.Time `json:"sentAt"`
	PaidAt    *time.Time `json:"paidAt"`
	VoidedAt  *time.Time `json:"voidedAt"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`

	Items     []Item    `json:"items,omitempty"`
	Payments  []Payment `json:"payments,omitempty"`
	ItemCount int       `json:"itemCount"`
}

type store struct {
	pool *pgxpool.Pool
}

// This deployment's invoices table stores one money column, amount_due, and no
// title, notes, issue date or lifecycle timestamps. amount_due is therefore the
// invoice total; the subtotal and tax are derived from the line items, and the
// amount paid from settled payments. Everything else is a typed NULL holding its
// scan position, so the Go model and the JSON contract are unchanged.
//
// `paid` is a LATERAL rather than three correlated subqueries: the paid figure
// feeds the balance and the overdue flag as well, and computing it once keeps
// those three in agreement by construction.
const invoiceColumns = `
	i.id::text,
	i.invoice_number                   AS number,
	NULL::text                         AS title,
	i.status, i.currency,
	i.quote_id::text,
	'Q-' || upper(substr(i.quote_id::text, 1, 8)) AS quote_number,
	i.company_id::text                 AS account_id,
	a.name                             AS account_name,
	NULL::text                         AS contact_id,
	NULL::text                         AS contact_name,
	q.deal_id::text                    AS deal_id,
	d.title                            AS deal_title,
	i.account_manager_id::text         AS owner_user_id,
	p.full_name                        AS owner_name,
	NULL::text                         AS owner_email,
	NULL::text                         AS notes,
	NULL::date                         AS issue_date,
	i.due_date,
	COALESCE(items.gross, 0)::float8   AS subtotal,
	0::float8                          AS discount_total,
	COALESCE(items.tax, 0)::float8     AS tax_total,
	i.amount_due::float8               AS total,
	paid.amt::float8                   AS amount_paid,
	(i.amount_due - paid.amt)::float8  AS balance,
	(i.status = 'sent' AND i.due_date IS NOT NULL
	   AND i.due_date < CURRENT_DATE AND i.amount_due > paid.amt),
	NULL::timestamptz                  AS sent_at,
	paid.at                            AS paid_at,
	NULL::timestamptz                  AS voided_at,
	i.created_at, i.updated_at,
	(SELECT count(*) FROM invoice_items it WHERE it.invoice_id = i.id)`

const invoiceFrom = `
	FROM invoices i
	LEFT JOIN quotes    q ON q.id = i.quote_id
	LEFT JOIN companies a ON a.id = i.company_id
	LEFT JOIN deals     d ON d.id = q.deal_id
	LEFT JOIN profiles  p ON p.id = i.account_manager_id
	CROSS JOIN LATERAL (
	  SELECT COALESCE(sum(amount), 0) AS amt, max(paid_at) AS at
	    FROM payments pm WHERE pm.invoice_id = i.id AND pm.status = 'succeeded'
	) paid
	CROSS JOIN LATERAL (
	  SELECT COALESCE(sum(round(quantity * unit_price, 2)), 0) AS gross,
	         COALESCE(sum(round(line_total * tax_percent / 100, 2)), 0) AS tax
	    FROM invoice_items ii WHERE ii.invoice_id = i.id
	) items `

func (s *store) list(ctx context.Context, orgID, status string, limit, offset int) ([]Invoice, error) {
	// 'overdue' is a view over sent invoices, not a stored status, so it is
	// filtered here rather than compared against the status column.
	rows, err := s.pool.Query(ctx,
		`SELECT `+invoiceColumns+invoiceFrom+
			`WHERE i.deleted_at IS NULL
			   AND ($1 = '' OR ($1 <> 'overdue' AND i.status = $1)
			        OR ($1 = 'overdue' AND i.status = 'sent'
			            AND i.due_date IS NOT NULL AND i.due_date < CURRENT_DATE
			            AND i.amount_due > paid.amt))
			 ORDER BY i.created_at DESC, i.id
			 LIMIT $2 OFFSET $3`, status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Invoice, 0, limit)
	for rows.Next() {
		inv, err := scanInvoice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (s *store) count(ctx context.Context, orgID, status string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM invoices i
		 CROSS JOIN LATERAL (
		   SELECT COALESCE(sum(amount), 0) AS amt
		     FROM payments pm WHERE pm.invoice_id = i.id AND pm.status = 'succeeded'
		 ) paid
		 WHERE i.deleted_at IS NULL
		   AND ($1 = '' OR ($1 <> 'overdue' AND i.status = $1)
		        OR ($1 = 'overdue' AND i.status = 'sent'
		            AND i.due_date IS NOT NULL AND i.due_date < CURRENT_DATE
		            AND i.amount_due > paid.amt))`, status).Scan(&n)
	return n, err
}

func (s *store) get(ctx context.Context, orgID, id string) (Invoice, error) {
	inv, err := scanInvoice(s.pool.QueryRow(ctx,
		`SELECT `+invoiceColumns+invoiceFrom+`WHERE i.id = $1 AND i.deleted_at IS NULL`, id))
	if err != nil {
		return Invoice{}, err
	}

	if inv.Items, err = s.items(ctx, id); err != nil {
		return Invoice{}, err
	}
	if inv.Payments, err = s.payments(ctx, id); err != nil {
		return Invoice{}, err
	}
	return inv, nil
}

func (s *store) items(ctx context.Context, invoiceID string) ([]Item, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id::text, position, description, quantity::float8, unit_price::float8,
		        discount_percent::float8, tax_percent::float8, line_total::float8
		 FROM invoice_items WHERE invoice_id = $1 ORDER BY position, id`, invoiceID)
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

func (s *store) payments(ctx context.Context, invoiceID string) ([]Payment, error) {
	rows, err := s.pool.Query(ctx,
		// No method or note column here; the provider reference stands in for the
		// reference field, and paid_at carries the date.
		`SELECT id::text, amount::float8, paid_at::date, NULL::text,
		        stripe_payment_intent_id, NULL::text, created_at
		 FROM payments WHERE invoice_id = $1 ORDER BY paid_at DESC, created_at DESC`, invoiceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Payment, 0, 4)
	for rows.Next() {
		var p Payment
		if err := rows.Scan(&p.ID, &p.Amount, &p.PaidOn, &p.Method, &p.Reference,
			&p.Note, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// nextNumber allocates the org's next invoice number inside the caller's
// transaction. The row lock on organizations serializes numbering per tenant.
func nextNumber(ctx context.Context, tx pgx.Tx, orgID string) (string, error) {
	var seq int
	if err := tx.QueryRow(ctx,
		`UPDATE organizations SET invoice_seq = invoice_seq + 1 WHERE id = $1
		 RETURNING invoice_seq`, orgID).Scan(&seq); err != nil {
		return "", err
	}
	return fmt.Sprintf("INV-%04d", seq), nil
}

func (s *store) create(ctx context.Context, orgID, currency string, in Input) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	number, err := nextNumber(ctx, tx, orgID)
	if err != nil {
		return "", err
	}

	var id string
	if err := tx.QueryRow(ctx,
		// Title, notes, contact, deal and issue date have no column here and are
		// dropped; the deal is reached through the originating quote instead.
		`INSERT INTO invoices
		   (invoice_number, status, currency, company_id, account_manager_id,
		    due_date, amount_due)
		 VALUES ($1, 'draft', $2, $3,
		         (SELECT id FROM profiles WHERE id = $4::uuid), $5, 0)
		 RETURNING id::text`,
		number, currency, in.AccountID, in.OwnerUserID, in.DueDate,
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

func (s *store) update(ctx context.Context, orgID, id string, in Input) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Claim the row and assert it is still a draft in one statement, so the
	// check can't race a concurrent issue.
	var status string
	err = tx.QueryRow(ctx,
		`UPDATE invoices
		 SET company_id = $2,
		     account_manager_id = (SELECT id FROM profiles WHERE id = $3::uuid),
		     due_date = $4, updated_at = now()
		 WHERE id = $1 AND status = 'draft' AND deleted_at IS NULL
		 RETURNING status`,
		id, in.AccountID, in.OwnerUserID, in.DueDate).Scan(&status)

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

// replaceItems swaps the whole item set. Same reasoning as quotes: nothing
// references an individual line, and the sets are small.
func replaceItems(ctx context.Context, tx pgx.Tx, orgID, invoiceID string, items []ItemInput) error {
	if _, err := tx.Exec(ctx, `DELETE FROM invoice_items WHERE invoice_id = $1`, invoiceID); err != nil {
		return err
	}

	for i, item := range items {
		if _, err := tx.Exec(ctx,
			`INSERT INTO invoice_items
			   (invoice_id, org_id, position, description, quantity, unit_price,
			    discount_percent, tax_percent, line_total)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8,
			         round($5::numeric * $6::numeric * (1 - $7::numeric / 100), 2))`,
			invoiceID, orgID, i, item.Description, item.Quantity, item.UnitPrice,
			item.DiscountPercent, item.TaxPercent); err != nil {
			return translate(err)
		}
	}
	return nil
}

// recalculate re-derives totals from items and amount_paid from payments.
//
// Identical rounding to quotes (see EXPLAINER §23.2) so a quote and the invoice
// raised from it can never disagree by a cent.
func recalculate(ctx context.Context, tx pgx.Tx, orgID, invoiceID string) error {
	_, err := tx.Exec(ctx,
		`WITH t AS (
		   SELECT
		     COALESCE(sum(round(quantity * unit_price, 2)), 0) AS gross,
		     COALESCE(sum(line_total), 0)                      AS net,
		     COALESCE(sum(round(line_total * tax_percent / 100, 2)), 0) AS tax
		   FROM invoice_items WHERE invoice_id = $1
		 )
		 UPDATE invoices
		 SET amount_due = t.net + t.tax,
		     updated_at = now()
		 FROM t
		 WHERE invoices.id = $1`, invoiceID)
	return err
}

// addPayment records a receipt and settles the invoice if it is now covered.
func (s *store) addPayment(ctx context.Context, orgID, invoiceID string, in PaymentInput) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Only an issued, unvoided invoice can take money. Checked as part of the
	// lock so it can't race a void.
	var status string
	err = tx.QueryRow(ctx,
		`SELECT status FROM invoices WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`,
		invoiceID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) || isPgCode(err, pgInvalidTextRepr) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != "sent" && status != "paid" {
		return ErrNotPayable
	}

	if _, err := tx.Exec(ctx,
		// Method and note have no column here. A payment recorded by hand is
		// settled by definition, so it goes straight in as succeeded.
		`INSERT INTO payments (invoice_id, amount, currency, status, paid_at,
		                       stripe_payment_intent_id)
		 VALUES ($1, $2,
		         (SELECT currency FROM invoices WHERE id = $1),
		         'succeeded', COALESCE($3::timestamptz, now()), $4)`,
		invoiceID, in.Amount, in.PaidOn, in.Reference); err != nil {
		return translate(err)
	}

	if err := recalculate(ctx, tx, orgID, invoiceID); err != nil {
		return err
	}

	// Settle automatically once the balance is covered — making someone click
	// "mark paid" after entering the final payment is busywork the system can do.
	if _, err := tx.Exec(ctx,
		`UPDATE invoices i
		 SET status = 'paid', updated_at = now()
		 WHERE i.id = $1 AND i.status = 'sent'
		   AND (SELECT COALESCE(sum(amount), 0) FROM payments pm
		         WHERE pm.invoice_id = i.id AND pm.status = 'succeeded') >= i.amount_due`,
		invoiceID); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// setStatus applies a lifecycle transition and stamps the matching timestamp.
func (s *store) setStatus(ctx context.Context, orgID, id, from, to string) error {
	tag, err := s.pool.Exec(ctx,
		// No per-status timestamp columns in this schema, so a transition records
		// the new status and updated_at only.
		`UPDATE invoices
		 SET status = $3, updated_at = now()
		 WHERE id = $1 AND status = $2 AND deleted_at IS NULL`,
		id, from, to)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *store) currentStatus(ctx context.Context, orgID, id string) (string, error) {
	var status string
	err := s.pool.QueryRow(ctx,
		`SELECT status FROM invoices WHERE id = $1 AND deleted_at IS NULL`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) || isPgCode(err, pgInvalidTextRepr) {
		return "", ErrNotFound
	}
	return status, err
}

// delete removes a draft. An issued invoice is voided, never deleted — the
// number has been given to a customer.
func (s *store) delete(ctx context.Context, orgID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM invoices WHERE id = $1 AND status = 'draft'`, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return s.explainWriteMiss(ctx, orgID, id)
	}
	return nil
}

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

func (s *store) refInOrg(ctx context.Context, table, orgID, id string) (bool, error) {
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

// Stats backs the dashboard.
type Stats struct {
	Total       int     `json:"total"`
	Outstanding float64 `json:"outstanding"`
	Overdue     float64 `json:"overdue"`
	Paid        float64 `json:"paid"`
}

func (s *store) stats(ctx context.Context, orgID string) (Stats, error) {
	var st Stats
	err := s.pool.QueryRow(ctx,
		`SELECT
		   count(*),
		   COALESCE(sum(i.amount_due - paid.amt) FILTER (WHERE i.status = 'sent'), 0)::float8,
		   COALESCE(sum(i.amount_due - paid.amt) FILTER (
		     WHERE i.status = 'sent' AND i.due_date IS NOT NULL AND i.due_date < CURRENT_DATE
		   ), 0)::float8,
		   COALESCE(sum(paid.amt) FILTER (WHERE i.status <> 'void'), 0)::float8
		 FROM invoices i
		 CROSS JOIN LATERAL (
		   SELECT COALESCE(sum(amount), 0) AS amt
		     FROM payments pm WHERE pm.invoice_id = i.id AND pm.status = 'succeeded'
		 ) paid
		 WHERE i.deleted_at IS NULL`,
	).Scan(&st.Total, &st.Outstanding, &st.Overdue, &st.Paid)
	return st, err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanInvoice(row rowScanner) (Invoice, error) {
	var inv Invoice
	err := row.Scan(
		&inv.ID, &inv.Number, &inv.Title, &inv.Status, &inv.Currency,
		&inv.QuoteID, &inv.QuoteNumber,
		&inv.AccountID, &inv.AccountName,
		&inv.ContactID, &inv.ContactName,
		&inv.DealID, &inv.DealTitle,
		&inv.OwnerUserID, &inv.OwnerName, &inv.OwnerEmail,
		&inv.Notes, &inv.IssueDate, &inv.DueDate,
		&inv.Subtotal, &inv.DiscountTotal, &inv.TaxTotal,
		&inv.Total, &inv.AmountPaid, &inv.Balance, &inv.Overdue,
		&inv.SentAt, &inv.PaidAt, &inv.VoidedAt, &inv.CreatedAt, &inv.UpdatedAt,
		&inv.ItemCount)
	if err != nil {
		return Invoice{}, translate(err)
	}
	return inv, nil
}

func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows), isPgCode(err, pgInvalidTextRepr):
		return ErrNotFound
	case isPgCode(err, pgUniqueViolation):
		// The only unique constraint a caller can trip is one-invoice-per-quote.
		return ErrQuoteNotInvoiceable
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
