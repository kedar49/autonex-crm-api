package quotes

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/go-crm/services/internal/activities"
	"github.com/go-crm/services/internal/pdf"
	"github.com/go-crm/services/pkg/httpx"
	"github.com/go-crm/services/pkg/middleware"
)

// Handler exposes the quotes module's HTTP API.
type Handler struct {
	svc *Service
	// pool is here only to write system events to the activity log; the
	// module's own data always goes through svc.
	pool   *pgxpool.Pool
	secret string
}

// NewHandler wires the quotes service to the pgx pool.
func NewHandler(pool *pgxpool.Pool, secret string) *Handler {
	return &Handler{svc: NewService(pool), pool: pool, secret: secret}
}

// Routes returns the quotes sub-router, mounted at /api/v1/quotes.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequireJWT(h.secret))

	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{id}", h.get)
	r.Get("/{id}/pdf", h.downloadPDF)
	r.Put("/{id}", h.update)
	r.Delete("/{id}", h.remove)
	// Separate from PUT: a lifecycle move is not a field edit, and it is allowed
	// on documents that are no longer editable.
	r.Post("/{id}/status", h.setStatus)
	return r
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	page, err := h.svc.List(r.Context(), middleware.OrgID(r.Context()), q.Get("status"), limit, offset)
	if err != nil {
		writeErr(w, err, "could not list quotes")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	quote, err := h.svc.Get(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err, "could not load quote")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, quote)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	quote, err := h.svc.Create(r.Context(), middleware.OrgID(r.Context()), in)
	if err != nil {
		writeErr(w, err, "could not create quote")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, quote)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	quote, err := h.svc.Update(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id"), in)
	if err != nil {
		writeErr(w, err, "could not update quote")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, quote)
}

func (h *Handler) setStatus(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status string `json:"status"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	quote, err := h.svc.SetStatus(ctx, middleware.OrgID(ctx), chi.URLParam(r, "id"), in.Status)
	if err != nil {
		writeErr(w, err, "could not update the quote's status")
		return
	}

	activities.Log(ctx, h.pool, activities.Entry{
		OrgID: middleware.OrgID(ctx), Actor: middleware.UserID(ctx),
		QuoteID: quote.ID, DealID: deref(quote.DealID), AccountID: deref(quote.AccountID),
		Subject: "Quote " + quote.Number + " marked " + quote.Status,
	})
	httpx.WriteJSON(w, http.StatusOK, quote)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id")); err != nil {
		writeErr(w, err, "could not delete quote")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) downloadPDF(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	quote, err := h.svc.Get(ctx, middleware.OrgID(ctx), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err, "could not load quote")
		return
	}

	pdfGen := pdf.NewGenerator()

	items := make([]pdf.POItemData, len(quote.Items))
	for i, item := range quote.Items {
		items[i] = pdf.POItemData{
			SrNo:        i + 1,
			Description: item.Description,
			Qty:         item.Quantity,
			UnitPrice:   item.UnitPrice,
			Amount:      item.Quantity * item.UnitPrice,
		}
	}

	accountName := "Vendor Account"
	if quote.AccountName != nil && *quote.AccountName != "" {
		accountName = *quote.AccountName
	}

	validUntil := ""
	if quote.ValidUntil != nil {
		validUntil = quote.ValidUntil.Format("02-Jan-2006")
	}

	poData := pdf.POData{
		PONumber:        quote.Number,
		PODate:          quote.CreatedAt.Format("02-Jan-2006"),
		DeliveryDate:    validUntil,
		PaymentTerms:    "Net 30",
		PlaceOfDelivery: "Powai, Mumbai",
		PreparedBy:      "Autonex CRM System",
		VendorName:      accountName,
		ContactPerson:   "Procurement Team",
		VendorAddress:   "Mumbai, Maharashtra",
		VendorGSTIN:     "27AAACC1234D1Z5",
		VendorPhone:     "+91 98765 00000",
		VendorEmail:     "vendor@example.com",
		Items:           items,
		Subtotal:        quote.Subtotal,
		TaxRate:         18.0,
		TaxAmount:       quote.TaxTotal,
		GrandTotal:      quote.Total,
		Notes:           []string{"PO subject to Autonex standard terms of procurement.", "All deliveries must include packing slip with PO Number."},
		ExecutionBy:     "Nikhil Gawade",
		ExecutionTitle:  "Founder & CEO",
		SignatoryName:   "NIKHIL SUNIL GAWADE",
		SignatoryRole:   "Director",
		SignatoryDIN:    "11217265",
	}

	reader, err := pdfGen.GeneratePOHTML(ctx, poData)
	if err != nil {
		httpx.WriteServerError(w, "pdf rendering failed", err)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", "inline; filename=\"PO-"+quote.Number+".html\"")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}

func writeErr(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "quote not found")
	case errors.Is(err, ErrNotDraft):
		httpx.WriteError(w, http.StatusConflict,
			"this quote has been issued — revise it back to draft to make changes")
	case errors.Is(err, ErrRefNotFound):
		httpx.WriteError(w, http.StatusBadRequest,
			"a referenced account, contact, deal or owner is not part of your organization")
	case IsValidation(err):
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httpx.WriteServerError(w, fallback, err)
	}
}
