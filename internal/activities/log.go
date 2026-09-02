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

	// This deployment stores the subject of an activity as a single
	// (entity_type, entity_id) pair rather than one column per entity type, and
	// has no subject column — the headline goes into the body. entity_type,
	// entity_id and author_id are all NOT NULL, so an event that cannot name all
	// three is skipped rather than attempted.
	entityType, entityID := entityOf(e)
	if entityType == "" || e.Actor == "" {
		return
	}

	body := e.Subject
	if e.Body != "" {
		body = e.Subject + "\n\n" + e.Body
	}

	// INSERT ... SELECT FROM profiles rather than a subquery in VALUES: an actor
	// with no profile row inserts nothing, instead of failing the NOT NULL on
	// author_id.
	tag, err := pool.Exec(ctx,
		`INSERT INTO activities (entity_type, entity_id, type, body, author_id)
		 SELECT $1, $2::uuid, $3, $4, p.id
		   FROM profiles p WHERE p.id = $5::uuid`,
		entityType, entityID, resolveKind(e.Kind), body, e.Actor)

	switch {
	case err != nil:
		log.Printf("activity log: %q for org %s: %v", e.Subject, e.OrgID, err)
	case tag.RowsAffected() == 0:
		log.Printf("activity log: %q skipped, actor %s has no profile", e.Subject, e.Actor)
	}
}

// entityOf reduces the per-entity ids on an Entry to the single
// (entity_type, entity_id) pair this schema stores. An entry naming more than
// one is recorded against the first, which is the most specific in practice.
func entityOf(e Entry) (string, string) {
	switch {
	case e.LeadID != "":
		return "lead", e.LeadID
	case e.DealID != "":
		return "deal", e.DealID
	case e.AccountID != "":
		return "company", e.AccountID
	case e.ContactID != "":
		return "contact", e.ContactID
	case e.QuoteID != "":
		return "quote", e.QuoteID
	case e.InvoiceID != "":
		return "invoice", e.InvoiceID
	}
	return "", ""
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
