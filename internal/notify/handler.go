package notify

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/go-crm/services/pkg/httpx"
	"github.com/go-crm/services/pkg/middleware"
)

type Handler struct {
	store  *Store
	secret string
}

func NewHandler(store *Store, secret string) *Handler {
	return &Handler{store: store, secret: secret}
}

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequireJWT(h.secret))

	r.Get("/", h.list)
	r.Patch("/{id}/read", h.markRead)
	r.Post("/read-all", h.markAllRead)
	r.Post("/subscribe", h.subscribe)
	r.Delete("/unsubscribe", h.unsubscribe)
	r.Get("/stream", h.stream)

	return r
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	orgID := middleware.OrgID(r.Context())
	userID := middleware.UserID(r.Context())

	resp, err := h.store.ListNotifications(r.Context(), orgID, userID, limit, offset)
	if err != nil {
		httpx.WriteServerError(w, "could not list notifications", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) markRead(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgID(r.Context())
	userID := middleware.UserID(r.Context())
	id := chi.URLParam(r, "id")

	if err := h.store.MarkAsRead(r.Context(), orgID, userID, id); err != nil {
		httpx.WriteServerError(w, "could not mark notification read", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) markAllRead(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgID(r.Context())
	userID := middleware.UserID(r.Context())

	if err := h.store.MarkAllAsRead(r.Context(), orgID, userID); err != nil {
		httpx.WriteServerError(w, "could not mark all notifications read", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) subscribe(w http.ResponseWriter, r *http.Request) {
	var sub PushSubscription
	if !httpx.DecodeJSON(w, r, &sub) {
		return
	}
	sub.OrgID = middleware.OrgID(r.Context())
	sub.UserID = middleware.UserID(r.Context())
	sub.UserAgent = r.UserAgent()

	if err := h.store.SavePushSubscription(r.Context(), sub); err != nil {
		httpx.WriteServerError(w, "could not save push subscription", err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) unsubscribe(w http.ResponseWriter, r *http.Request) {
	var sub PushSubscription
	if !httpx.DecodeJSON(w, r, &sub) {
		return
	}
	userID := middleware.UserID(r.Context())

	if err := h.store.DeletePushSubscription(r.Context(), userID, sub.Endpoint); err != nil {
		httpx.WriteServerError(w, "could not remove push subscription", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) stream(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	h.store.ServeSSE(w, r, userID)
}
