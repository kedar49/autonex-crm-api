// Package dashboard aggregates the other domain modules into the portal's
// landing-page summary. It owns no tables of its own — it reads through the
// modules that do, so a metric can never drift from its source.
package dashboard

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/go-crm/services/internal/leads"
	"github.com/go-crm/services/pkg/httpx"
	"github.com/go-crm/services/pkg/middleware"
)

// StageSummary is one kanban column's contribution to the pipeline.
type StageSummary struct {
	Stage string  `json:"stage"`
	Count int     `json:"count"`
	Value float64 `json:"value"`
}

// Summary is the whole landing page in one response, so the dashboard makes a
// single request rather than fanning out to every module.
type Summary struct {
	Contacts int            `json:"contacts"`
	Members  int            `json:"members"`
	Leads    int            `json:"leads"`
	Stages   []StageSummary `json:"stages"`
	// OpenPipeline is the value of everything not yet won or lost.
	OpenPipeline float64 `json:"openPipeline"`
	WonValue     float64 `json:"wonValue"`
}

// Handler exposes GET /api/v1/dashboard.
type Handler struct {
	pool   *pgxpool.Pool
	leads  *leads.Service
	secret string
}

// NewHandler wires the dashboard to the pool and the modules it reads from.
func NewHandler(pool *pgxpool.Pool, secret string) *Handler {
	return &Handler{pool: pool, leads: leads.NewService(pool), secret: secret}
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
	orgID := middleware.OrgID(ctx)

	sum, err := h.build(ctx, orgID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "could not load the dashboard")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sum)
}

func (h *Handler) build(ctx context.Context, orgID string) (Summary, error) {
	stats, err := h.leads.Stats(ctx, orgID)
	if err != nil {
		return Summary{}, err
	}

	// Index the per-stage rows so every stage appears, including empty ones —
	// a board column missing from the summary would read as a bug in the UI.
	byStage := make(map[string]leads.Stats, len(stats))
	for _, s := range stats {
		byStage[s.Stage] = s
	}

	sum := Summary{Stages: make([]StageSummary, 0, len(leads.Stages))}
	for _, stage := range leads.Stages {
		s := byStage[stage]
		sum.Stages = append(sum.Stages, StageSummary{Stage: stage, Count: s.Count, Value: s.Value})
		sum.Leads += s.Count

		switch stage {
		case "won":
			sum.WonValue += s.Value
		case "lost":
			// Neither open nor won.
		default:
			sum.OpenPipeline += s.Value
		}
	}

	if err := h.pool.QueryRow(ctx,
		`SELECT count(*) FROM contacts WHERE org_id = $1`, orgID).Scan(&sum.Contacts); err != nil {
		return Summary{}, err
	}
	if err := h.pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE org_id = $1`, orgID).Scan(&sum.Members); err != nil {
		return Summary{}, err
	}
	return sum, nil
}
