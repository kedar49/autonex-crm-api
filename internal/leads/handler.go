package leads

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/go-crm/services/pkg/httpx"
	"github.com/go-crm/services/pkg/middleware"
)

// Handler exposes the leads module's HTTP API.
type Handler struct {
	svc    *Service
	secret string
}

// NewHandler wires the leads service to the pgx pool.
func NewHandler(pool *pgxpool.Pool, secret string) *Handler {
	return &Handler{svc: NewService(pool), secret: secret}
}

// Routes returns the leads sub-router, mounted at /api/v1/leads.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequireJWT(h.secret))

	r.Get("/", h.board)
	r.Post("/", h.create)
	r.Get("/{id}", h.get)
	r.Put("/{id}", h.update)
	r.Delete("/{id}", h.remove)
	// Separate from PUT: a drag-and-drop is a reorder, not a field edit, and the
	// board sends it on every drop.
	r.Patch("/{id}/move", h.move)
	// POST, not PATCH: converting creates two new records, it doesn't edit this one.
	r.Post("/{id}/convert", h.convert)
	return r
}

func (h *Handler) convert(w http.ResponseWriter, r *http.Request) {
	var in ConvertInput
	// An empty body is valid — every override is optional.
	if r.ContentLength > 0 && !httpx.DecodeJSON(w, r, &in) {
		return
	}
	result, err := h.svc.Convert(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"), in)
	if err != nil {
		writeErr(w, err, "could not convert lead")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, result)
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
	lead, err := h.svc.Get(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err, "could not load lead")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, lead)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	lead, err := h.svc.Create(r.Context(), middleware.OrgID(r.Context()), in)
	if err != nil {
		writeErr(w, err, "could not create lead")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, lead)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	lead, err := h.svc.Update(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"), in)
	if err != nil {
		writeErr(w, err, "could not update lead")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, lead)
}

func (h *Handler) move(w http.ResponseWriter, r *http.Request) {
	var mv Move
	if !httpx.DecodeJSON(w, r, &mv) {
		return
	}
	lead, err := h.svc.Move(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"), mv)
	if err != nil {
		writeErr(w, err, "could not move lead")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, lead)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id")); err != nil {
		writeErr(w, err, "could not delete lead")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeErr(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "lead not found")
	case errors.Is(err, ErrOwnerNotFound):
		httpx.WriteError(w, http.StatusBadRequest, "that owner is not a member of your organization")
	case errors.Is(err, ErrAlreadyConverted):
		httpx.WriteError(w, http.StatusConflict, "this lead has already been converted")
	case IsValidation(err):
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httpx.WriteServerError(w, fallback, err)
	}
}
