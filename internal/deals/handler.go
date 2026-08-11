package deals

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/go-crm/services/internal/activities"
	"github.com/go-crm/services/pkg/httpx"
	"github.com/go-crm/services/pkg/middleware"
)

// Handler exposes the deals module's HTTP API.
type Handler struct {
	svc *Service
	// pool is here only to write system events to the activity log; the
	// module's own data always goes through svc.
	pool   *pgxpool.Pool
	secret string
}

// NewHandler wires the deals service to the pgx pool.
func NewHandler(pool *pgxpool.Pool, secret string) *Handler {
	return &Handler{svc: NewService(pool), pool: pool, secret: secret}
}

// Routes returns the deals sub-router, mounted at /api/v1/deals.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequireJWT(h.secret))

	r.Get("/", h.board)
	r.Post("/", h.create)
	r.Get("/{id}", h.get)
	r.Put("/{id}", h.update)
	r.Delete("/{id}", h.remove)
	// Separate from PUT: a drag-and-drop is a reorder, not a field edit.
	r.Patch("/{id}/move", h.move)
	return r
}

func (h *Handler) board(w http.ResponseWriter, r *http.Request) {
	board, err := h.svc.Board(r.Context(), middleware.OrgID(r.Context()))
	if err != nil {
		httpx.WriteServerError(w, "could not load the board", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, board)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	deal, err := h.svc.Get(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err, "could not load deal")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, deal)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	deal, err := h.svc.Create(ctx, middleware.OrgID(ctx), in)
	if err != nil {
		writeErr(w, err, "could not create deal")
		return
	}

	activities.Log(ctx, h.pool, activities.Entry{
		OrgID: middleware.OrgID(ctx), Actor: middleware.UserID(ctx),
		DealID: deal.ID, AccountID: deref(deal.AccountID), ContactID: deref(deal.ContactID),
		Subject: "Deal created", Body: deal.Title,
	})
	httpx.WriteJSON(w, http.StatusCreated, deal)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	deal, err := h.svc.Update(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"), in)
	if err != nil {
		writeErr(w, err, "could not update deal")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, deal)
}

func (h *Handler) move(w http.ResponseWriter, r *http.Request) {
	var mv Move
	if !httpx.DecodeJSON(w, r, &mv) {
		return
	}
	ctx := r.Context()
	deal, err := h.svc.Move(ctx, middleware.OrgID(ctx), chi.URLParam(r, "id"), mv)
	if err != nil {
		writeErr(w, err, "could not move deal")
		return
	}

	activities.Log(ctx, h.pool, activities.Entry{
		OrgID: middleware.OrgID(ctx), Actor: middleware.UserID(ctx),
		DealID:  deal.ID,
		Subject: "Deal moved to " + StageLabel(deal.Stage),
	})
	httpx.WriteJSON(w, http.StatusOK, deal)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id")); err != nil {
		writeErr(w, err, "could not delete deal")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeErr(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "deal not found")
	case errors.Is(err, ErrRefNotFound):
		httpx.WriteError(w, http.StatusBadRequest,
			"that owner or contact is not part of your organization")
	case IsValidation(err):
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httpx.WriteServerError(w, fallback, err)
	}
}
