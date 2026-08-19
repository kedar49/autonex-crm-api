package dashboard

import (
	"context"
	"time"
)

// Attention is one thing that is going wrong, or about to.
//
// Deliberately flat and pre-formatted: the client renders these as a list
// without knowing what a quote or an invoice is. Adding a source later means a
// new arm of the UNION, not a new component.
type Attention struct {
	// Kind drives the icon and the link target.
	Kind string `json:"kind"`
	ID   string `json:"id"`
	// Label is the thing ("Ivan Overdue", "INV-004"); Detail is its context.
	Label  string `json:"label"`
	Detail string `json:"detail"`
	// Days is negative for overdue and positive for upcoming, so one sort field
	// orders the whole queue by urgency.
	Days   int     `json:"days"`
	Amount float64 `json:"amount"`
}

// Recent is one timeline entry, flattened for the dashboard's activity strip.
type Recent struct {
	Kind    string    `json:"kind"`
	Subject string    `json:"subject"`
	Body    string    `json:"body"`
	Actor   string    `json:"actor"`
	At      time.Time `json:"at"`
}

// attentionLimit keeps the queue a queue. A dashboard that lists forty problems
// is a second inbox, not a summary — the rest live on their own pages.
const attentionLimit = 6

// attention gathers everything overdue or imminent across the modules.
//
// One UNION rather than four round trips: the arms are cheap index lookups, and
// the ranking has to happen across all of them anyway — doing it in Go would
// mean over-fetching every arm just to throw most of it away.
func (h *Handler) attention(ctx context.Context, orgID string) ([]Attention, error) {
	rows, err := h.pool.Query(ctx, `
		WITH items AS (
		  -- Leads whose follow-up has come due. Terminal stages are finished, so
		  -- they can never be late.
		  SELECT 'lead'::text AS kind,
		         l.id::text   AS id,
		         btrim(l.first_name || ' ' || coalesce(l.last_name, '')) AS label,
		         coalesce(a.name, l.company, '')                         AS detail,
		         (l.follow_up_at - CURRENT_DATE)::int                    AS days,
		         coalesce(l.value, 0)::float8                            AS amount
		    FROM leads l
		    LEFT JOIN accounts a ON a.id = l.account_id
		   WHERE l.org_id = $1
		     AND l.stage NOT IN ('converted', 'dropped')
		     AND l.follow_up_at IS NOT NULL
		     AND l.follow_up_at <= CURRENT_DATE + 2

		  UNION ALL

		  -- Quotes still awaiting an answer as their validity runs out.
		  SELECT 'quote', q.id::text, q.number,
		         coalesce(ac.name, q.title, ''),
		         (q.valid_until - CURRENT_DATE)::int,
		         q.total::float8
		    FROM quotes q
		    LEFT JOIN accounts ac ON ac.id = q.account_id
		   WHERE q.org_id = $1
		     AND q.status IN ('draft', 'sent')
		     AND q.valid_until IS NOT NULL
		     AND q.valid_until <= CURRENT_DATE + 7

		  UNION ALL

		  -- Money owed. Balance is derived, so a part-paid invoice still counts
		  -- for exactly what is left on it.
		  SELECT 'invoice', i.id::text, i.number,
		         coalesce(ac.name, i.title, ''),
		         (i.due_date - CURRENT_DATE)::int,
		         (i.total - i.amount_paid)::float8
		    FROM invoices i
		    LEFT JOIN accounts ac ON ac.id = i.account_id
		   WHERE i.org_id = $1
		     AND i.status NOT IN ('paid', 'void')
		     AND i.due_date IS NOT NULL
		     AND i.due_date <= CURRENT_DATE + 3
		     AND i.total > i.amount_paid
		)
		SELECT kind, id, label, detail, days, amount
		  FROM items
		 ORDER BY days ASC, amount DESC
		 LIMIT $2`, orgID, attentionLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]Attention, 0, attentionLimit)
	for rows.Next() {
		var a Attention
		if err := rows.Scan(&a.Kind, &a.ID, &a.Label, &a.Detail, &a.Days, &a.Amount); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

const recentLimit = 6

// recent reads the tail of the timeline across every record in the org.
func (h *Handler) recent(ctx context.Context, orgID string) ([]Recent, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT a.kind, a.subject, coalesce(a.body, ''), coalesce(u.name, u.email, ''),
		       a.occurred_at
		  FROM activities a
		  LEFT JOIN users u ON u.id = a.created_by
		 WHERE a.org_id = $1
		 ORDER BY a.occurred_at DESC, a.created_at DESC
		 LIMIT $2`, orgID, recentLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]Recent, 0, recentLimit)
	for rows.Next() {
		var r Recent
		if err := rows.Scan(&r.Kind, &r.Subject, &r.Body, &r.Actor, &r.At); err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	return items, rows.Err()
}
