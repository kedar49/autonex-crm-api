package accounts

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/go-crm/services/pkg/httpx"
	"github.com/go-crm/services/pkg/middleware"
	"github.com/go-crm/services/pkg/paging"
)

// Handler exposes the accounts module's HTTP API.
type Handler struct {
	svc    *Service
	secret string
}

// NewHandler wires the accounts service to the pgx pool.
func NewHandler(pool *pgxpool.Pool, secret string) *Handler {
	return &Handler{svc: newService(pool), secret: secret}
}

// Routes returns the accounts sub-router, mounted at /api/v1/accounts.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequireJWT(h.secret))

	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{id}", h.get)
	r.Put("/{id}", h.update)
	r.Delete("/{id}", h.remove)
	r.Get("/{id}/profile", h.getProfile)
	r.Put("/{id}/profile", h.updateProfile)
	return r
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	limit, offset := paging.Params(r)

	page, err := h.svc.List(r.Context(), middleware.OrgID(r.Context()), limit, offset)
	if err != nil {
		httpx.WriteServerError(w, "could not list accounts", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	account, err := h.svc.Get(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err, "could not load account")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, account)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	account, err := h.svc.Create(r.Context(), middleware.OrgID(r.Context()), in)
	if err != nil {
		writeErr(w, err, "could not create account")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, account)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	account, err := h.svc.Update(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"), in)
	if err != nil {
		writeErr(w, err, "could not update account")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, account)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id")); err != nil {
		writeErr(w, err, "could not delete account")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) getProfile(w http.ResponseWriter, r *http.Request) {
	payload, err := h.svc.GetFullProfile(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err, "could not load company profile")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
}

func (h *Handler) updateProfile(w http.ResponseWriter, r *http.Request) {
	var in ProfileInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	payload, err := h.svc.UpdateFullProfile(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"), in)
	if err != nil {
		writeErr(w, err, "could not update company profile")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
}

func writeErr(w http.ResponseWriter, err error, fallback string) {
	httpx.WriteDomainError(w, err, fallback,
		httpx.Rule{Err: ErrNotFound, Status: http.StatusNotFound,
			Message: "account not found"},
		httpx.Rule{Err: ErrInUse, Status: http.StatusConflict,
			Message: "unlink its contacts and deals before deleting this account"},
		httpx.Rule{Err: ErrOwnerNotFound, Status: http.StatusBadRequest,
			Message: "that owner is not a member of your organization"},
	)
}
