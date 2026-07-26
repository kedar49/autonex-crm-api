package org

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/go-crm/services/pkg/config"
	"github.com/go-crm/services/pkg/httpx"
	"github.com/go-crm/services/pkg/middleware"
)

// Handler exposes the organization/teammate HTTP API.
type Handler struct {
	svc *Service
	cfg config.Config
}

// NewHandler wires the org service to the pgx pool and config.
func NewHandler(pool *pgxpool.Pool, cfg config.Config) *Handler {
	return &Handler{svc: newService(pool, cfg), cfg: cfg}
}

// Routes returns the org sub-router, mounted at /api/v1/org.
//
// Accepting an invitation is public by necessity — the invitee has no session
// yet; the token in the link is the credential.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/invitations/accept", h.accept)

	r.Group(func(pr chi.Router) {
		pr.Use(middleware.RequireJWT(h.cfg.JWTSecret))
		pr.Get("/members", h.members)
		pr.Get("/invitations", h.invitations)
		pr.Post("/invitations", h.invite)
		pr.Delete("/invitations/{id}", h.revoke)
	})
	return r
}

func (h *Handler) members(w http.ResponseWriter, r *http.Request) {
	members, err := h.svc.Members(r.Context(), middleware.OrgID(r.Context()))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "could not list members")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, members)
}

func (h *Handler) invitations(w http.ResponseWriter, r *http.Request) {
	invites, err := h.svc.PendingInvitations(r.Context(), middleware.OrgID(r.Context()))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "could not list invitations")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, invites)
}

func (h *Handler) invite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}

	ctx := r.Context()
	inv, err := h.svc.Invite(ctx, middleware.OrgID(ctx), middleware.UserID(ctx), in.Email)
	if err != nil {
		writeErr(w, err, "could not create invitation")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, inv)
}

func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Revoke(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id")); err != nil {
		writeErr(w, err, "could not revoke invitation")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) accept(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token    string `json:"token"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}

	session, err := h.svc.Accept(r.Context(), in.Token, in.Name, in.Password)
	if err != nil {
		writeErr(w, err, "could not accept invitation")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, session)
}

func writeErr(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "invitation not found")
	case errors.Is(err, ErrAlreadyMember):
		httpx.WriteError(w, http.StatusConflict, "that email already belongs to an account")
	case errors.Is(err, ErrAlreadyInvited):
		httpx.WriteError(w, http.StatusConflict, "that email already has a pending invitation")
	case errors.Is(err, ErrInviteInvalid):
		// Deliberately vague: unknown, expired and already-used all look alike,
		// so the endpoint can't be used to probe for live invitations.
		httpx.WriteError(w, http.StatusBadRequest, "this invitation is invalid or has expired")
	case IsValidation(err):
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httpx.WriteError(w, http.StatusInternalServerError, fallback)
	}
}
