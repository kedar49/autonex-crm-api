package invoices

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// defaultPaymentTerms is how far out the due date is set when the caller doesn't
// say. Thirty days is the common default; it is a starting point, not a policy.
const defaultPaymentTerms = 30 * 24 * time.Hour

// FromQuote raises an invoice from an accepted quote, copying the header and
// every line.
//
// The copy is deliberate rather than a reference. A quote can be revised after
// being accepted-and-invoiced (declined → draft → sent again), and an invoice
// that silently changed its amounts because the source document moved would be
// indefensible. The link back is kept for provenance only.
//
// Boundary note: like leads → deals conversion (§20.3), this module reads
// another's tables directly. Copying through the quotes service would need a
// shared unit-of-work, since each store owns its own pool and this has to be one
// transaction — a numbered invoice with no lines would be worse than no invoice.
func (s *Service) FromQuote(ctx context.Context, orgID, quoteID string, dueDate *time.Time) (Invoice, error) {
	id, err := s.store.fromQuote(ctx, orgID, quoteID, dueDate)
	if err != nil {
		return Invoice{}, err
	}
	return s.store.get(ctx, orgID, id)
}

func (s *store) fromQuote(
	ctx context.Context, orgID, quoteID string, dueDate *time.Time,
) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Only an approved quote can be billed, and the row is locked so the status
	// can't change underneath the copy.
	//
	// A quote header here carries its company, deal, author and status; the
	// currency lives on the current version row. There is no title, contact or
	// notes to copy.
	var (
		currency string
		status   string
		company  *string
		owner    *string
	)
	err = tx.QueryRow(ctx,
		`SELECT q.status, COALESCE(v.currency, 'USD'),
		        q.company_id::text, q.created_by::text
		   FROM quotes q
		   LEFT JOIN quote_versions v ON v.quote_id = q.id AND v.is_current
		  WHERE q.id = $1 AND q.deleted_at IS NULL
		    FOR UPDATE OF q`,
		quoteID,
	).Scan(&status, &currency, &company, &owner)

	if errors.Is(err, pgx.ErrNoRows) || isPgCode(err, pgInvalidTextRepr) {
		return "", ErrQuoteNotInvoiceable
	}
	if err != nil {
		return "", err
	}
	if status != "approved" {
		return "", ErrQuoteNotInvoiceable
	}

	number, err := nextNumber(ctx, tx, orgID)
	if err != nil {
		return "", err
	}

	due := dueDate
	if due == nil {
		d := time.Now().Add(defaultPaymentTerms)
		due = &d
	}

	var id string
	if err := tx.QueryRow(ctx,
		`INSERT INTO invoices
		   (invoice_number, status, currency, quote_id, company_id,
		    account_manager_id, due_date, amount_due)
		 VALUES ($1, 'draft', $2, $3, $4,
		         (SELECT id FROM profiles WHERE id = $5::uuid), $6, 0)
		 RETURNING id::text`,
		number, currency, quoteID, company, owner, due,
	).Scan(&id); err != nil {
		// A unique violation here is the partial index: this quote already has a
		// live invoice.
		return "", translate(err)
	}

	// Copy the lines, including their computed line totals. Recomputing from the
	// same inputs would give the same answer, but copying keeps the invoice a
	// faithful record of the quote at the moment it was billed.
	if _, err := tx.Exec(ctx,
		`INSERT INTO invoice_items
		   (invoice_id, org_id, position, description, quantity, unit_price,
		    discount_percent, tax_percent, line_total)
		 SELECT $2, $1, position, description, quantity, unit_price,
		        discount_percent, tax_percent, line_total
		 FROM quote_items WHERE quote_id = $3
		 ORDER BY position, id`,
		orgID, id, quoteID); err != nil {
		return "", translate(err)
	}

	if err := recalculate(ctx, tx, orgID, id); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}
