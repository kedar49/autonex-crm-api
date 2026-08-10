package invoices

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/go-crm/services/pkg/httpx"
	"github.com/go-crm/services/pkg/middleware"
)

// Handler exposes the invoices module's HTTP API.
type Handler struct {
	svc    *Service
	secret string
}

// NewHandler wires the invoices service to the pgx pool.
func NewHandler(pool *pgxpool.Pool, secret string) *Handler {
	return &Handler{svc: NewService(pool), secret: secret}
}

// Routes returns the invoices sub-router, mounted at /api/v1/invoices.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequireJWT(h.secret))

	r.Get("/", h.list)
	r.Post("/", h.create)
	// Raising from a quote is a different operation from creating one by hand,
	// so it gets its own route rather than an overloaded POST body.
	r.Post("/from-quote", h.fromQuote)
	r.Get("/{id}", h.get)
	r.Put("/{id}", h.update)
	r.Delete("/{id}", h.remove)
	r.Post("/{id}/status", h.setStatus)
	r.Post("/{id}/payments", h.recordPayment)
	return r
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	page, err := h.svc.List(r.Context(), middleware.OrgID(r.Context()), q.Get("status"), limit, offset)
	if err != nil {
		writeErr(w, err, "could not list invoices")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	invoice, err := h.svc.Get(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err, "could not load invoice")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, invoice)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	invoice, err := h.svc.Create(r.Context(), middleware.OrgID(r.Context()), in)
	if err != nil {
		writeErr(w, err, "could not create invoice")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, invoice)
}

func (h *Handler) fromQuote(w http.ResponseWriter, r *http.Request) {
	var in struct {
		QuoteID string     `json:"quoteId"`
		DueDate *time.Time `json:"dueDate"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	invoice, err := h.svc.FromQuote(r.Context(), middleware.OrgID(r.Context()), in.QuoteID, in.DueDate)
	if err != nil {
		writeErr(w, err, "could not raise an invoice from that quote")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, invoice)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	invoice, err := h.svc.Update(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"), in)
	if err != nil {
		writeErr(w, err, "could not update invoice")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, invoice)
}

func (h *Handler) setStatus(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status string `json:"status"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	invoice, err := h.svc.SetStatus(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"), in.Status)
	if err != nil {
		writeErr(w, err, "could not update the invoice's status")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, invoice)
}

func (h *Handler) recordPayment(w http.ResponseWriter, r *http.Request) {
	var in PaymentInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	invoice, err := h.svc.RecordPayment(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"), in)
	if err != nil {
		writeErr(w, err, "could not record that payment")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, invoice)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id")); err != nil {
		writeErr(w, err, "could not delete invoice")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeErr(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "invoice not found")
	case errors.Is(err, ErrNotDraft):
		httpx.WriteError(w, http.StatusConflict,
			"this invoice has been issued — void it and raise a new one instead")
	case errors.Is(err, ErrNotPayable):
		httpx.WriteError(w, http.StatusConflict,
			"only an issued invoice can take a payment")
	case errors.Is(err, ErrQuoteNotInvoiceable):
		httpx.WriteError(w, http.StatusConflict,
			"that quote must be accepted, and not already invoiced")
	case errors.Is(err, ErrRefNotFound):
		httpx.WriteError(w, http.StatusBadRequest,
			"a referenced account, contact, deal or owner is not part of your organization")
	case IsValidation(err):
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httpx.WriteServerError(w, fallback, err)
	}
}
