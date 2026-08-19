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

	// Only an accepted quote can be billed, and the row is locked so the status
	// can't change underneath the copy.
	var (
		title    *string
		currency string
		status   string
		account  *string
		contact  *string
		deal     *string
		owner    *string
		notes    *string
	)
	err = tx.QueryRow(ctx,
		`SELECT title, currency, status, account_id::text, contact_id::text,
		        deal_id::text, owner_user_id::text, notes
		 FROM quotes WHERE org_id = $1 AND id = $2 FOR UPDATE`,
		orgID, quoteID,
	).Scan(&title, &currency, &status, &account, &contact, &deal, &owner, &notes)

	if errors.Is(err, pgx.ErrNoRows) || isPgCode(err, pgInvalidTextRepr) {
		return "", ErrQuoteNotInvoiceable
	}
	if err != nil {
		return "", err
	}
	if status != "accepted" {
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
		   (org_id, number, title, currency, quote_id, account_id, contact_id,
		    deal_id, owner_user_id, notes, issue_date, due_date)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, CURRENT_DATE, $11)
		 RETURNING id::text`,
		orgID, number, title, currency, quoteID, account, contact, deal, owner, notes, due,
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
