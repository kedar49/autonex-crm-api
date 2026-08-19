package audit

import (
	"net/http"
	"strconv"

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

	return r
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	orgStr := middleware.OrgID(r.Context())
	orgID, err := uuid.Parse(orgStr)
	if err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, "invalid organization")
		return
	}

	var entityType *string
	if et := r.URL.Query().Get("entity_type"); et != "" {
		entityType = &et
	}

	var entityID *uuid.UUID
	if eid := r.URL.Query().Get("entity_id"); eid != "" {
		if parsed, err := uuid.Parse(eid); err == nil {
			entityID = &parsed
		}
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	logs, err := h.svc.List(r.Context(), orgID, entityType, entityID, int32(limit), int32(offset))
	if err != nil {
		httpx.WriteServerError(w, "could not list audit logs", err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, logs)
}
