package followups

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-crm/services/pkg/httpx"
	"github.com/go-crm/services/pkg/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	svc    *Service
	secret string
}

func NewHandler(pool *pgxpool.Pool, secret string) *Handler {
	return &Handler{svc: NewService(pool), secret: secret}
}

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequireJWT(h.secret))

	r.Get("/", h.handleList)
	r.Post("/", h.handleCreate)
	r.Post("/{id}/complete", h.handleComplete)

	return r
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	orgStr := middleware.OrgID(r.Context())
	orgID, err := uuid.Parse(orgStr)
	if err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, "invalid organization")
		return
	}

	items, err := h.svc.List(r.Context(), orgID, nil, nil)
	if err != nil {
		httpx.WriteServerError(w, "could not list followups", err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, items)
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	orgStr := middleware.OrgID(r.Context())
	orgID, err := uuid.Parse(orgStr)
	if err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, "invalid organization")
		return
	}

	var input CreateFollowUpInput
	if !httpx.DecodeJSON(w, r, &input) {
		return
	}

	item, err := h.svc.Create(r.Context(), orgID, input)
	if err != nil {
		httpx.WriteServerError(w, "could not create followup", err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, item)
}

func (h *Handler) handleComplete(w http.ResponseWriter, r *http.Request) {
	orgStr := middleware.OrgID(r.Context())
	orgID, err := uuid.Parse(orgStr)
	if err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, "invalid organization")
		return
	}

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid follow-up id")
		return
	}

	item, err := h.svc.Complete(r.Context(), orgID, id)
	if err != nil {
		httpx.WriteServerError(w, "could not complete followup", err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, item)
}
