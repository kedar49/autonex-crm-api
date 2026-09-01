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
		         coalesce(l.contact_name, '')                AS label,
		         coalesce(a.name, '')                        AS detail,
		         (l.next_follow_up_date - CURRENT_DATE)::int AS days,
		         coalesce(l.value_estimate, 0)::float8       AS amount
		    FROM leads l
		    LEFT JOIN companies a ON a.id = l.company_id
		   WHERE l.deleted_at IS NULL
		     AND l.status NOT IN ('closed', 'not interested')
		     AND l.next_follow_up_date IS NOT NULL
		     AND l.next_follow_up_date <= CURRENT_DATE + 2

		  UNION ALL

		  -- Quotes still awaiting an answer as their validity runs out. The
		  -- number is derived from the id, and the total from the current version.
		  SELECT 'quote', q.id::text,
		         'Q-' || upper(substr(q.id::text, 1, 8)),
		         coalesce(ac.name, d.title, ''),
		         (q.valid_until - CURRENT_DATE)::int,
		         coalesce(v.total, 0)::float8
		    FROM quotes q
		    LEFT JOIN companies      ac ON ac.id = q.company_id
		    LEFT JOIN deals          d  ON d.id  = q.deal_id
		    LEFT JOIN quote_versions v  ON v.quote_id = q.id AND v.is_current
		   WHERE q.deleted_at IS NULL
		     AND q.status IN ('draft', 'sent')
		     AND q.valid_until IS NOT NULL
		     AND q.valid_until <= CURRENT_DATE + 7

		  UNION ALL

		  -- Money owed. The balance is derived from settled payments, so a
		  -- part-paid invoice still counts for exactly what is left on it.
		  SELECT 'invoice', i.id::text, i.invoice_number,
		         coalesce(ac.name, ''),
		         (i.due_date - CURRENT_DATE)::int,
		         (i.amount_due - paid.amt)::float8
		    FROM invoices i
		    LEFT JOIN companies ac ON ac.id = i.company_id
		    CROSS JOIN LATERAL (
		      SELECT COALESCE(sum(amount), 0) AS amt
		        FROM payments pm
		       WHERE pm.invoice_id = i.id AND pm.status = 'succeeded'
		    ) paid
		   WHERE i.deleted_at IS NULL
		     AND i.status NOT IN ('paid', 'void')
		     AND i.due_date IS NOT NULL
		     AND i.due_date <= CURRENT_DATE + 3
		     AND i.amount_due > paid.amt
		)
		SELECT kind, id, label, detail, days, amount
		  FROM items
		 ORDER BY days ASC, amount DESC
		 LIMIT $1`, attentionLimit)
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
		-- No subject column in this schema; the entity type stands in as the
		-- one-word heading the timeline row shows above its body.
		SELECT a.type, a.entity_type, coalesce(a.body, ''),
		       coalesce(p.full_name, ''), a.occurred_at
		  FROM activities a
		  LEFT JOIN profiles p ON p.id = a.author_id
		 ORDER BY a.occurred_at DESC, a.created_at DESC
		 LIMIT $1`, recentLimit)
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
