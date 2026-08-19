// Package dashboard aggregates the other domain modules into the portal's
// landing-page summary. It owns no tables of its own — it reads through the
// modules that do, so a metric can never drift from its source.
package dashboard

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/go-crm/services/internal/deals"
	"github.com/go-crm/services/internal/invoices"
	"github.com/go-crm/services/internal/leads"
	"github.com/go-crm/services/internal/quotes"
	"github.com/go-crm/services/pkg/httpx"
	"github.com/go-crm/services/pkg/middleware"
)

// StageSummary is one kanban column's contribution to a pipeline.
type StageSummary struct {
	Stage string  `json:"stage"`
	Count int     `json:"count"`
	Value float64 `json:"value"`
}

// Pipeline is one board's totals: per-stage rows plus the two roll-ups the tiles
// show. `Open` excludes won and lost; `Won` is closed value.
type Pipeline struct {
	Total  int            `json:"total"`
	Open   float64        `json:"open"`
	Won    float64        `json:"won"`
	Stages []StageSummary `json:"stages"`
}

// Summary is the whole landing page in one response, so the dashboard makes a
// single request rather than fanning out to every module.
type Summary struct {
	Contacts int      `json:"contacts"`
	Members  int      `json:"members"`
	Leads    Pipeline `json:"leads"`
	Deals    Pipeline `json:"deals"`
	// Quotes reuses Pipeline: "open" is everything not accepted or declined, and
	// "won" is accepted value — the same question, asked of documents.
	Quotes Pipeline `json:"quotes"`
	// Billing is not a pipeline: what matters is money owed, not stage counts.
	Invoices invoices.Stats `json:"invoices"`

	// The two lists the command centre leads with: what needs doing, and what
	// just happened.
	Attention []Attention `json:"attention"`
	Recent    []Recent    `json:"recent"`
}

// Handler exposes GET /api/v1/dashboard.
type Handler struct {
	pool     *pgxpool.Pool
	leads    *leads.Service
	deals    *deals.Service
	quotes   *quotes.Service
	invoices *invoices.Service
	secret   string
}

// NewHandler wires the dashboard to the pool and the modules it reads from.
func NewHandler(pool *pgxpool.Pool, secret string) *Handler {
	return &Handler{
		pool:     pool,
		leads:    leads.NewService(pool),
		deals:    deals.NewService(pool),
		quotes:   quotes.NewService(pool),
		invoices: invoices.NewService(pool),
		secret:   secret,
	}
}

// Routes returns the dashboard sub-router, mounted at /api/v1/dashboard.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequireJWT(h.secret))
	r.Get("/", h.summary)
	return r
}

func (h *Handler) summary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	sum, err := h.build(ctx, middleware.OrgID(ctx))
	if err != nil {
		httpx.WriteServerError(w, "could not load the dashboard", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sum)
}

func (h *Handler) build(ctx context.Context, orgID string) (Summary, error) {
	var sum Summary

	leadStats, err := h.leads.Stats(ctx, orgID)
	if err != nil {
		return Summary{}, err
	}
	counts := make(map[string]StageSummary, len(leadStats))
	for _, s := range leadStats {
		counts[s.Stage] = StageSummary{Stage: s.Stage, Count: s.Count, Value: s.Value}
	}
	sum.Leads = rollUp(leads.Stages, "converted", []string{"dropped"}, counts)

	dealStats, err := h.deals.Stats(ctx, orgID)
	if err != nil {
		return Summary{}, err
	}
	counts = make(map[string]StageSummary, len(dealStats))
	for _, s := range dealStats {
		counts[s.Stage] = StageSummary{Stage: s.Stage, Count: s.Count, Value: s.Amount}
	}
	sum.Deals = rollUp(deals.Stages, "won", []string{"lost"}, counts)

	quoteStats, err := h.quotes.Stats(ctx, orgID)
	if err != nil {
		return Summary{}, err
	}
	counts = make(map[string]StageSummary, len(quoteStats))
	for _, s := range quoteStats {
		counts[s.Status] = StageSummary{Stage: s.Status, Count: s.Count, Value: s.Value}
	}
	sum.Quotes = rollUp(quotes.Statuses, "accepted", []string{"declined", "expired"}, counts)

	if sum.Invoices, err = h.invoices.Stats(ctx, orgID); err != nil {
		return Summary{}, err
	}

	if err := h.pool.QueryRow(ctx,
		`SELECT count(*) FROM contacts WHERE org_id = $1`, orgID).Scan(&sum.Contacts); err != nil {
		return Summary{}, err
	}
	if err := h.pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE org_id = $1`, orgID).Scan(&sum.Members); err != nil {
		return Summary{}, err
	}

	if sum.Attention, err = h.attention(ctx, orgID); err != nil {
		return Summary{}, err
	}
	if sum.Recent, err = h.recent(ctx, orgID); err != nil {
		return Summary{}, err
	}
	return sum, nil
}

// rollUp walks the canonical stage order so every stage appears, including empty
// ones — a board column missing from the summary would read as a UI bug.
//
// `won` and `closed` are parameters rather than the literals "won"/"lost",
// because quotes end in "accepted"/"declined"+"expired". Hardcoding the board
// vocabulary would have quietly counted every draft and declined quote as open
// pipeline.
func rollUp(stages []string, won string, closed []string, found map[string]StageSummary) Pipeline {
	isClosed := make(map[string]bool, len(closed))
	for _, s := range closed {
		isClosed[s] = true
	}

	p := Pipeline{Stages: make([]StageSummary, 0, len(stages))}

	for _, stage := range stages {
		row := found[stage]
		row.Stage = stage
		p.Stages = append(p.Stages, row)
		p.Total += row.Count

		switch {
		case stage == won:
			p.Won += row.Value
		case isClosed[stage]:
			// Neither open nor won.
		default:
			p.Open += row.Value
		}
	}
	return p
}
