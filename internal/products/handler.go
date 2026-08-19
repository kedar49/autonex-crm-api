package products

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
	r.Get("/{id}", h.handleGet)
	r.Delete("/{id}", h.handleDelete)

	return r
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	orgStr := middleware.OrgID(r.Context())
	orgID, err := uuid.Parse(orgStr)
	if err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, "invalid organization")
		return
	}

	var category *string
	if c := r.URL.Query().Get("category"); c != "" {
		category = &c
	}

	items, err := h.svc.List(r.Context(), orgID, category, nil)
	if err != nil {
		httpx.WriteServerError(w, "could not list products", err)
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

	var input CreateProductInput
	if !httpx.DecodeJSON(w, r, &input) {
		return
	}

	p, err := h.svc.Create(r.Context(), orgID, input)
	if err != nil {
		httpx.WriteServerError(w, "could not create product", err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, p)
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
	orgStr := middleware.OrgID(r.Context())
	orgID, err := uuid.Parse(orgStr)
	if err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, "invalid organization")
		return
	}

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid product id")
		return
	}

	p, err := h.svc.Get(r.Context(), orgID, id)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "product not found")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	orgStr := middleware.OrgID(r.Context())
	orgID, err := uuid.Parse(orgStr)
	if err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, "invalid organization")
		return
	}

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid product id")
		return
	}

	if err := h.svc.Delete(r.Context(), orgID, id); err != nil {
		httpx.WriteServerError(w, "could not delete product", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
