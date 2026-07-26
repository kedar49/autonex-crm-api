package contacts

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/go-crm/services/pkg/httpx"
	"github.com/go-crm/services/pkg/middleware"
)

// Handler exposes the contacts module's HTTP API.
type Handler struct {
	svc    *Service
	secret string
}

// NewHandler wires the contacts service to the pgx pool. secret is the JWT
// signing key used by the route guard.
func NewHandler(pool *pgxpool.Pool, secret string) *Handler {
	return &Handler{svc: newService(pool), secret: secret}
}

// Routes returns the contacts sub-router, mounted at /api/v1/contacts.
// Every route is authenticated — there is no public view of CRM data.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequireJWT(h.secret))

	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{id}", h.get)
	r.Put("/{id}", h.update)
	r.Delete("/{id}", h.remove)
	return r
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))   // 0 → service default
	offset, _ := strconv.Atoi(q.Get("offset")) // 0 → first page

	page, err := h.svc.List(r.Context(), middleware.OrgID(r.Context()), limit, offset)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "could not list contacts")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	c, err := h.svc.Get(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		h.writeErr(w, err, "could not load contact")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	c, err := h.svc.Create(r.Context(), middleware.OrgID(r.Context()), in)
	if err != nil {
		h.writeErr(w, err, "could not create contact")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, c)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	c, err := h.svc.Update(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"), in)
	if err != nil {
		h.writeErr(w, err, "could not update contact")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id")); err != nil {
		h.writeErr(w, err, "could not delete contact")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeErr maps a domain error to its status code; fallback is the 500 message.
func (h *Handler) writeErr(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "contact not found")
	case errors.Is(err, ErrEmailTaken):
		httpx.WriteError(w, http.StatusConflict, "a contact with that email already exists")
	case errors.Is(err, ErrAccountNotFound):
		httpx.WriteError(w, http.StatusBadRequest, "unknown account")
	case IsValidation(err):
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httpx.WriteError(w, http.StatusInternalServerError, fallback)
	}
}
