package activities

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/go-crm/services/pkg/httpx"
	"github.com/go-crm/services/pkg/middleware"
)

// Handler exposes the activity log's HTTP API.
type Handler struct {
	svc    *Service
	secret string
}

// NewHandler wires the activities service to the pgx pool.
func NewHandler(pool *pgxpool.Pool, secret string) *Handler {
	return &Handler{svc: NewService(pool), secret: secret}
}

// Routes returns the activities sub-router, mounted at /api/v1/activities.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequireJWT(h.secret))

	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Put("/{id}", h.update)
	r.Delete("/{id}", h.remove)
	return r
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))

	items, err := h.svc.List(r.Context(), middleware.OrgID(r.Context()), Filter{
		LeadID:    q.Get("leadId"),
		DealID:    q.Get("dealId"),
		AccountID: q.Get("accountId"),
		ContactID: q.Get("contactId"),
		QuoteID:   q.Get("quoteId"),
		InvoiceID: q.Get("invoiceId"),
		Limit:     limit,
	})
	if err != nil {
		writeErr(w, err, "could not load the timeline")
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
	activity, err := h.svc.Create(ctx, middleware.OrgID(ctx), middleware.UserID(ctx), in)
	if err != nil {
		writeErr(w, err, "could not log that activity")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, activity)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	activity, err := h.svc.Update(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"), in)
	if err != nil {
		writeErr(w, err, "could not update that activity")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, activity)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id")); err != nil {
		writeErr(w, err, "could not delete that activity")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeErr(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "activity not found")
	case errors.Is(err, ErrSystemImmutable):
		httpx.WriteError(w, http.StatusConflict,
			"this entry was recorded automatically and cannot be changed")
	case errors.Is(err, ErrRefNotFound):
		httpx.WriteError(w, http.StatusBadRequest,
			"a referenced record is not part of your organization")
	case IsValidation(err):
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httpx.WriteServerError(w, fallback, err)
	}
}
