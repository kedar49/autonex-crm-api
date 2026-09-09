package delivery

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/go-crm/services/pkg/apperr"
	"github.com/go-crm/services/pkg/httpx"
	"github.com/go-crm/services/pkg/middleware"
)

// maxUploadBytes caps an uploaded sheet at 8 MiB. A tracker export is tens of
// kilobytes; anything near this is a mistake or an attack, and either way the
// answer is the same.
const maxUploadBytes = 8 << 20

// Handler exposes the delivery tracker's HTTP API.
type Handler struct {
	svc    *Service
	secret string
}

// NewHandler wires the tracker service to the pgx pool. secret is the JWT
// signing key used by the route guard.
func NewHandler(pool *pgxpool.Pool, secret string) *Handler {
	return &Handler{svc: newService(pool), secret: secret}
}

// Routes returns the tracker sub-router, mounted at /api/v1/delivery.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequireJWT(h.secret))

	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Put("/{id}", h.update)
	r.Delete("/{id}", h.remove)
	r.Post("/reorder", h.reorder)

	// Two steps on purpose: an upload is previewed, then the user accepts what
	// they saw. Nothing an import does is written before that second call.
	r.Post("/import/preview", h.importPreview)
	r.Post("/import/commit", h.importCommit)
	return r
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	page, err := h.svc.List(r.Context(), middleware.OrgID(r.Context()))
	if err != nil {
		httpx.WriteServerError(w, "could not load the delivery tracker", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	row, err := h.svc.Create(r.Context(), middleware.OrgID(r.Context()), middleware.UserID(r.Context()), in)
	if err != nil {
		h.writeErr(w, err, "could not add that row")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, row)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	row, err := h.svc.Update(r.Context(), middleware.OrgID(r.Context()),
		middleware.UserID(r.Context()), chi.URLParam(r, "id"), in)
	if err != nil {
		h.writeErr(w, err, "could not save that row")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, row)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	err := h.svc.Delete(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		h.writeErr(w, err, "could not delete that row")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) reorder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	if err := h.svc.Reorder(r.Context(), middleware.OrgID(r.Context()), body.IDs); err != nil {
		h.writeErr(w, err, "could not reorder the tracker")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) importPreview(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "that upload could not be read (is it under 8 MB?)")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	file, header, err := r.FormFile("file")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "attach the spreadsheet as the \"file\" field")
		return
	}
	defer func() { _ = file.Close() }()

	preview, err := h.svc.Preview(r.Context(), middleware.OrgID(r.Context()), header.Filename, file)
	if err != nil {
		h.writeErr(w, err, "could not read that spreadsheet")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, preview)
}

func (h *Handler) importCommit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Rows []Input `json:"rows"`
	}
	// A confirmed import is many rows at once, so it needs a larger body than the
	// shared 64 KiB JSON cap allows.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}

	result, err := h.svc.Commit(r.Context(), middleware.OrgID(r.Context()),
		middleware.UserID(r.Context()), body.Rows)
	if err != nil {
		h.writeErr(w, err, "could not apply that import")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

// writeErr maps a domain error to its status code; fallback is the 500 message.
func (h *Handler) writeErr(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "that row no longer exists")
	case errors.Is(err, ErrClientTaken):
		httpx.WriteError(w, http.StatusConflict, "another row already tracks that client")
	case errors.Is(err, ErrNoHeader):
		httpx.WriteError(w, http.StatusBadRequest,
			"could not find a header row — the sheet needs a row of column names, including one for the client")
	case errors.Is(err, ErrNoClientColumn):
		httpx.WriteError(w, http.StatusBadRequest,
			"that sheet has no \"Client\" column, so its rows cannot be matched")
	case apperr.IsValidation(err):
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httpx.WriteServerError(w, fallback, err)
	}
}
