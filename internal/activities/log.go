package activities

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Entry is one timeline row written from inside another operation — usually a
// system event, occasionally a note the person typed alongside it.
type Entry struct {
	OrgID   string
	Subject string
	// Body is optional detail ("₹45,00,000 · Rohan Mehta").
	Body string
	// Actor is the user who caused it, if any. System jobs leave it empty.
	Actor string

	// Kind defaults to "system". Set it to a human kind ("note", "call", …) when
	// the row is something a person wrote rather than something the app did —
	// system rows render quieter and are deliberately not editable or deletable,
	// which is wrong for a note the user typed and may want to fix.
	Kind string

	LeadID    string
	DealID    string
	AccountID string
	ContactID string
	QuoteID   string
	InvoiceID string
}

// Log records a system event.
//
// **Best effort by design.** It is called after a state change has already been
// committed, so a failure here must never fail the operation the user asked for
// — losing a timeline row is bad, refusing to move a deal because the timeline
// write failed is worse. Failures are logged, not returned.
//
// This is a package-level function taking a pool rather than a service handed to
// every module: it keeps the call sites one line, and there is nothing to inject
// or configure.
func Log(ctx context.Context, pool *pgxpool.Pool, e Entry) {
	if e.OrgID == "" || e.Subject == "" {
		return
	}

	_, err := pool.Exec(ctx,
		`INSERT INTO activities
		   (org_id, kind, subject, body, created_by,
		    lead_id, deal_id, account_id, contact_id, quote_id, invoice_id)
		 VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, '')::uuid,
		         NULLIF($6, '')::uuid, NULLIF($7, '')::uuid, NULLIF($8, '')::uuid,
		         NULLIF($9, '')::uuid, NULLIF($10, '')::uuid, NULLIF($11, '')::uuid)`,
		e.OrgID, resolveKind(e.Kind), e.Subject, e.Body, e.Actor,
		e.LeadID, e.DealID, e.AccountID, e.ContactID, e.QuoteID, e.InvoiceID)

	if err != nil {
		log.Printf("activity log: %q for org %s: %v", e.Subject, e.OrgID, err)
	}
}

// resolveKind picks the kind to store. An unrecognised one would violate the
// table's CHECK constraint and lose the row entirely — and since Log is best
// effort, that loss would be silent — so anything unexpected becomes a system
// event rather than nothing at all.
func resolveKind(kind string) string {
	if kind == "" || !ValidKind(kind) {
		return "system"
	}
	return kind
}
