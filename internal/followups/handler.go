package followups

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-crm/services/pkg/httpx"
	"github.com/go-crm/services/pkg/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

// managerRoles is who may reach any route here — this module is the backend
// for the admin/manager-only Actions dashboard, not a rep-facing surface.
var managerRoles = []string{"owner", "admin", "account_manager"}

// Handler exposes the Actions module's HTTP API.
type Handler struct {
	svc    *Service
	secret string
}

// NewHandler wires the Actions service to the pgx pool. secret is the JWT
// signing key used by the route guard.
func NewHandler(pool *pgxpool.Pool, secret string) *Handler {
	return &Handler{svc: newService(pool), secret: secret}
}

// Routes returns the actions sub-router, mounted at /api/v1/actions.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequireJWT(h.secret))
	r.Use(middleware.RequireRole(managerRoles...))

	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{id}", h.get)
	r.Patch("/{id}", h.update)
	r.Delete("/{id}", h.remove)
	r.Post("/{id}/complete", h.complete)

	return r
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	f, ok := parseFilter(w, r)
	if !ok {
		return
	}

	items, err := h.svc.List(r.Context(), middleware.OrgID(r.Context()), f)
	if err != nil {
		httpx.WriteServerError(w, "could not list actions", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, items)
}

// parseFilter reads the client/status/due-date filters the Actions dashboard
// offers. Every param is optional.
func parseFilter(w http.ResponseWriter, r *http.Request) (Filter, bool) {
	q := r.URL.Query()
	f := Filter{
		AccountID:  q.Get("accountId"),
		LeadID:     q.Get("leadId"),
		Status:     q.Get("status"),
		AssignedTo: q.Get("assignedTo"),
	}

	if v := q.Get("status"); v != "" && !validStatus(v) {
		httpx.WriteError(w, http.StatusBadRequest, "status must be one of open, in_progress, done")
		return Filter{}, false
	}
	// Filters are cast to uuid in SQL, so a malformed value would surface
	// as a 500 rather than the client error it actually is.
	if v := f.AccountID; v != "" && !isUUID(v) {
		httpx.WriteError(w, http.StatusBadRequest, "accountId must be a UUID")
		return Filter{}, false
	}
	if v := f.LeadID; v != "" && !isUUID(v) {
		httpx.WriteError(w, http.StatusBadRequest, "leadId must be a UUID")
		return Filter{}, false
	}
	if v := f.AssignedTo; v != "" && !isUUID(v) {
		httpx.WriteError(w, http.StatusBadRequest, "assignedTo must be a UUID")
		return Filter{}, false
	}
	// excludeDone lets the dashboard's "active" views line up with their counts
	// without asking for two statuses in one request.
	if v := q.Get("excludeDone"); v != "" {
		if v != "true" && v != "false" {
			httpx.WriteError(w, http.StatusBadRequest, "excludeDone must be true or false")
			return Filter{}, false
		}
		f.ExcludeDone = v == "true"
	}
	if v := q.Get("dueBefore"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "dueBefore must be an RFC3339 timestamp")
			return Filter{}, false
		}
		f.DueBefore = &t
	}
	if v := q.Get("dueAfter"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "dueAfter must be an RFC3339 timestamp")
			return Filter{}, false
		}
		f.DueAfter = &t
	}
	return f, true
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	a, err := h.svc.Get(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		h.writeErr(w, err, "could not load action")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, a)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	a, err := h.svc.Create(r.Context(), middleware.OrgID(r.Context()), in)
	if err != nil {
		h.writeErr(w, err, "could not create action")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, a)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	a, err := h.svc.Update(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"), in)
	if err != nil {
		h.writeErr(w, err, "could not update action")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, a)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id")); err != nil {
		h.writeErr(w, err, "could not delete action")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) complete(w http.ResponseWriter, r *http.Request) {
	a, err := h.svc.Complete(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		h.writeErr(w, err, "could not complete action")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, a)
}

// writeErr maps a domain error to its status code; fallback is the 500 message.
func (h *Handler) writeErr(w http.ResponseWriter, err error, fallback string) {
	httpx.WriteDomainError(w, err, fallback,
		httpx.Rule{Err: ErrNotFound, Status: http.StatusNotFound,
			Message: "action not found"},
		httpx.Rule{Err: ErrAccountNotFound, Status: http.StatusBadRequest,
			Message: "unknown account"},
		httpx.Rule{Err: ErrLeadNotFound, Status: http.StatusBadRequest,
			Message: "unknown lead"},
		httpx.Rule{Err: ErrAssigneeNotFound, Status: http.StatusBadRequest,
			Message: "unknown assignee"},
	)
}

// isUUID reports whether s is a canonical 8-4-4-4-12 hex UUID. Query filters
// are interpolated into ::uuid casts, and Postgres answers a bad cast with an
// error, not an empty result.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}
