package dealtasks

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-crm/services/pkg/httpx"
	"github.com/go-crm/services/pkg/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Handler exposes the deal-task API.
//
// Unlike Actions, these routes carry no role guard: a task is the deal board's
// own checklist, and everyone who can see the board can tick one off. Who did
// so is recorded rather than restricted.
type Handler struct {
	svc    *Service
	secret string
}

func NewHandler(pool *pgxpool.Pool, secret string) *Handler {
	return &Handler{svc: newService(pool), secret: secret}
}

// Routes returns the sub-router mounted at /api/v1/deal-tasks.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequireJWT(h.secret))

	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Patch("/{id}", h.update)
	r.Delete("/{id}", h.remove)

	return r
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	dealID := r.URL.Query().Get("dealId")
	if dealID != "" && !isUUID(dealID) {
		httpx.WriteError(w, http.StatusBadRequest, "dealId must be a UUID")
		return
	}

	items, err := h.svc.List(r.Context(), dealID)
	if err != nil {
		httpx.WriteServerError(w, "could not list tasks", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, items)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	t, err := h.svc.Create(ctx, middleware.OrgID(ctx), middleware.UserID(ctx), in)
	if err != nil {
		h.writeErr(w, err, "could not create task")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, t)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	t, err := h.svc.Update(ctx, middleware.OrgID(ctx), middleware.UserID(ctx), chi.URLParam(r, "id"), in)
	if err != nil {
		h.writeErr(w, err, "could not update task")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		h.writeErr(w, err, "could not delete task")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) writeErr(w http.ResponseWriter, err error, fallback string) {
	httpx.WriteDomainError(w, err, fallback,
		httpx.Rule{Err: ErrNotFound, Status: http.StatusNotFound,
			Message: "task not found"},
		httpx.Rule{Err: ErrDealNotFound, Status: http.StatusBadRequest,
			Message: "unknown deal"},
		httpx.Rule{Err: ErrAssigneeNotFound, Status: http.StatusBadRequest,
			Message: "that assignee is not a member of your workspace"},
	)
}

// isUUID reports whether s is a canonical 8-4-4-4-12 hex UUID. The dealId
// filter is cast to ::uuid in SQL, and Postgres answers a bad cast with an
// error rather than an empty result.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}
