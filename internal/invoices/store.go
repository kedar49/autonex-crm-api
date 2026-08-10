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

const invoiceColumns = `
	i.id::text, i.number, i.title, i.status, i.currency,
	i.quote_id::text, q.number,
	i.account_id::text, a.name,
	i.contact_id::text, NULLIF(concat_ws(' ', c.first_name, c.last_name), ''),
	i.deal_id::text, d.title,
	i.owner_user_id::text, u.name, u.email,
	i.notes, i.issue_date, i.due_date,
	i.subtotal::float8, i.discount_total::float8, i.tax_total::float8,
	i.total::float8, i.amount_paid::float8,
	(i.total - i.amount_paid)::float8,
	(i.status = 'sent' AND i.due_date IS NOT NULL
	   AND i.due_date < CURRENT_DATE AND i.total > i.amount_paid),
	i.sent_at, i.paid_at, i.voided_at, i.created_at, i.updated_at,
	(SELECT count(*) FROM invoice_items it WHERE it.invoice_id = i.id)`

const invoiceFrom = `
	FROM invoices i
	LEFT JOIN quotes   q ON q.id = i.quote_id
	LEFT JOIN accounts a ON a.id = i.account_id
	LEFT JOIN contacts c ON c.id = i.contact_id
	LEFT JOIN deals    d ON d.id = i.deal_id
	LEFT JOIN users    u ON u.id = i.owner_user_id `

func (s *store) list(ctx context.Context, orgID, status string, limit, offset int) ([]Invoice, error) {
	// 'overdue' is a view over sent invoices, not a stored status, so it is
	// filtered here rather than compared against the status column.
	rows, err := s.pool.Query(ctx,
		`SELECT `+invoiceColumns+invoiceFrom+
			`WHERE i.org_id = $1
			   AND ($2 = '' OR ($2 <> 'overdue' AND i.status = $2)
			        OR ($2 = 'overdue' AND i.status = 'sent'
			            AND i.due_date IS NOT NULL AND i.due_date < CURRENT_DATE
			            AND i.total > i.amount_paid))
			 ORDER BY i.created_at DESC, i.id
			 LIMIT $3 OFFSET $4`, orgID, status, limit, offset)
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
		 WHERE i.org_id = $1
		   AND ($2 = '' OR ($2 <> 'overdue' AND i.status = $2)
		        OR ($2 = 'overdue' AND i.status = 'sent'
		            AND i.due_date IS NOT NULL AND i.due_date < CURRENT_DATE
		            AND i.total > i.amount_paid))`, orgID, status).Scan(&n)
	return n, err
}

func (s *store) get(ctx context.Context, orgID, id string) (Invoice, error) {
	inv, err := scanInvoice(s.pool.QueryRow(ctx,
		`SELECT `+invoiceColumns+invoiceFrom+`WHERE i.org_id = $1 AND i.id = $2`, orgID, id))
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
		`SELECT id::text, amount::float8, paid_on, method, reference, note, created_at
		 FROM payments WHERE invoice_id = $1 ORDER BY paid_on DESC, created_at DESC`, invoiceID)
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
		`INSERT INTO invoices
		   (org_id, number, title, currency, account_id, contact_id, deal_id,
		    owner_user_id, notes, issue_date, due_date)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 RETURNING id::text`,
		orgID, number, in.Title, currency, in.AccountID, in.ContactID, in.DealID,
		in.OwnerUserID, in.Notes, in.IssueDate, in.DueDate,
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
		 SET title = $3, account_id = $4, contact_id = $5, deal_id = $6,
		     owner_user_id = $7, notes = $8, issue_date = $9, due_date = $10,
		     updated_at = now()
		 WHERE org_id = $1 AND id = $2 AND status = 'draft'
		 RETURNING status`,
		orgID, id, in.Title, in.AccountID, in.ContactID, in.DealID,
		in.OwnerUserID, in.Notes, in.IssueDate, in.DueDate).Scan(&status)

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
		   FROM invoice_items WHERE invoice_id = $2
		 ), p AS (
		   SELECT COALESCE(sum(amount), 0) AS paid FROM payments WHERE invoice_id = $2
		 )
		 UPDATE invoices
		 SET subtotal = t.gross,
		     discount_total = t.gross - t.net,
		     tax_total = t.tax,
		     total = t.net + t.tax,
		     amount_paid = p.paid,
		     updated_at = now()
		 FROM t, p
		 WHERE invoices.org_id = $1 AND invoices.id = $2`, orgID, invoiceID)
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
		`SELECT status FROM invoices WHERE org_id = $1 AND id = $2 FOR UPDATE`,
		orgID, invoiceID).Scan(&status)
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
		`INSERT INTO payments (invoice_id, org_id, amount, paid_on, method, reference, note)
		 VALUES ($1, $2, $3, COALESCE($4, CURRENT_DATE), $5, $6, $7)`,
		invoiceID, orgID, in.Amount, in.PaidOn, in.Method, in.Reference, in.Note); err != nil {
		return translate(err)
	}

	if err := recalculate(ctx, tx, orgID, invoiceID); err != nil {
		return err
	}

	// Settle automatically once the balance is covered — making someone click
	// "mark paid" after entering the final payment is busywork the system can do.
	if _, err := tx.Exec(ctx,
		`UPDATE invoices
		 SET status = 'paid', paid_at = COALESCE(paid_at, now()), updated_at = now()
		 WHERE org_id = $1 AND id = $2 AND status = 'sent' AND amount_paid >= total`,
		orgID, invoiceID); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// setStatus applies a lifecycle transition and stamps the matching timestamp.
func (s *store) setStatus(ctx context.Context, orgID, id, from, to string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE invoices
		 SET status = $4,
		     sent_at   = CASE WHEN $4 = 'sent' THEN COALESCE(sent_at, now()) ELSE sent_at END,
		     issue_date = CASE WHEN $4 = 'sent' THEN COALESCE(issue_date, CURRENT_DATE) ELSE issue_date END,
		     paid_at   = CASE WHEN $4 = 'paid' THEN now() ELSE paid_at END,
		     voided_at = CASE WHEN $4 = 'void' THEN now() ELSE voided_at END,
		     updated_at = now()
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

func (s *store) currentStatus(ctx context.Context, orgID, id string) (string, error) {
	var status string
	err := s.pool.QueryRow(ctx,
		`SELECT status FROM invoices WHERE org_id = $1 AND id = $2`, orgID, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) || isPgCode(err, pgInvalidTextRepr) {
		return "", ErrNotFound
	}
	return status, err
}

// delete removes a draft. An issued invoice is voided, never deleted — the
// number has been given to a customer.
func (s *store) delete(ctx context.Context, orgID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM invoices WHERE org_id = $1 AND id = $2 AND status = 'draft'`, orgID, id)
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
		   COALESCE(sum(total - amount_paid) FILTER (WHERE status = 'sent'), 0)::float8,
		   COALESCE(sum(total - amount_paid) FILTER (
		     WHERE status = 'sent' AND due_date IS NOT NULL AND due_date < CURRENT_DATE
		   ), 0)::float8,
		   COALESCE(sum(amount_paid) FILTER (WHERE status <> 'void'), 0)::float8
		 FROM invoices WHERE org_id = $1`, orgID,
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
