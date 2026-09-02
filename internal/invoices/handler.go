package invoices

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/go-crm/services/internal/activities"
	"github.com/go-crm/services/internal/pdf"
	"github.com/go-crm/services/pkg/httpx"
	"github.com/go-crm/services/pkg/middleware"
)

// Handler exposes the invoices module's HTTP API.
type Handler struct {
	svc *Service
	// pool is here only to write system events to the activity log; the
	// module's own data always goes through svc.
	pool   *pgxpool.Pool
	secret string
}

// NewHandler wires the invoices service to the pgx pool.
func NewHandler(pool *pgxpool.Pool, secret string) *Handler {
	return &Handler{svc: NewService(pool), pool: pool, secret: secret}
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
	r.Get("/{id}/pdf", h.downloadPDF)
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
	ctx := r.Context()
	invoice, err := h.svc.SetStatus(ctx, middleware.OrgID(ctx), chi.URLParam(r, "id"), in.Status)
	if err != nil {
		writeErr(w, err, "could not update the invoice's status")
		return
	}

	activities.Log(ctx, h.pool, activities.Entry{
		OrgID: middleware.OrgID(ctx), Actor: middleware.UserID(ctx),
		InvoiceID: invoice.ID, DealID: deref(invoice.DealID), AccountID: deref(invoice.AccountID),
		Subject: "Invoice " + invoice.Number + " marked " + invoice.Status,
	})
	httpx.WriteJSON(w, http.StatusOK, invoice)
}

func (h *Handler) recordPayment(w http.ResponseWriter, r *http.Request) {
	var in PaymentInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	invoice, err := h.svc.RecordPayment(ctx, middleware.OrgID(ctx), chi.URLParam(r, "id"), in)
	if err != nil {
		writeErr(w, err, "could not record that payment")
		return
	}

	activities.Log(ctx, h.pool, activities.Entry{
		OrgID: middleware.OrgID(ctx), Actor: middleware.UserID(ctx),
		InvoiceID: invoice.ID, DealID: deref(invoice.DealID), AccountID: deref(invoice.AccountID),
		Subject: "Payment recorded on " + invoice.Number,
	})
	httpx.WriteJSON(w, http.StatusCreated, invoice)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), middleware.OrgID(r.Context()), chi.URLParam(r, "id")); err != nil {
		writeErr(w, err, "could not delete invoice")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) downloadPDF(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	invoice, err := h.svc.Get(ctx, middleware.OrgID(ctx), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err, "could not load invoice")
		return
	}

	pdfGen := pdf.NewGenerator()

	items := make([]pdf.InvoiceItemData, len(invoice.Items))
	for i, item := range invoice.Items {
		taxable := item.Quantity * item.UnitPrice
		taxAmt := taxable * (item.TaxPercent / 100.0)
		items[i] = pdf.InvoiceItemData{
			SrNo:         i + 1,
			Description:  item.Description,
			HSNSAC:       "998313",
			Qty:          item.Quantity,
			UnitPrice:    item.UnitPrice,
			TaxableValue: taxable,
			CGSTRate:     item.TaxPercent / 2.0,
			CGSTAmount:   taxAmt / 2.0,
			SGSTRate:     item.TaxPercent / 2.0,
			SGSTAmount:   taxAmt / 2.0,
			TotalAmount:  taxable + taxAmt,
		}
	}

	accountName := "Client Account"
	if invoice.AccountName != nil && *invoice.AccountName != "" {
		accountName = *invoice.AccountName
	}

	issueDateStr := invoice.CreatedAt.Format("02-Jan-2006")
	if invoice.IssueDate != nil {
		issueDateStr = invoice.IssueDate.Format("02-Jan-2006")
	}

	data := pdf.InvoiceData{
		IssuerName:     "AUTONEX AI 360 PRIVATE LIMITED",
		IssuerAddress:  "908, Lodha Supremus, Saki Vihar Road, Powai, Mumbai - 400072",
		IssuerContact:  "+91 98765 43210",
		IssuerEmail:    "billing@autonexai.com",
		IssuerGSTIN:    "27ABDCA3903H1ZX",
		IssuerPAN:      "ABDCA3903H",
		InvoiceNumber:  invoice.Number,
		InvoiceDate:    issueDateStr,
		ClientName:     accountName,
		ClientAddress:  "Mumbai, Maharashtra",
		ClientGSTIN:    "27AAACC1234D1Z5",
		Items:          items,
		TotalBeforeTax: invoice.Subtotal,
		CGSTTotal:      invoice.TaxTotal / 2.0,
		SGSTTotal:      invoice.TaxTotal / 2.0,
		TaxTotal:       invoice.TaxTotal,
		GrandTotal:     invoice.Total,
		AmountInWords:  pdf.ConvertNumberToWords(invoice.Total),
		BankName:       "HDFC Bank",
		BankBranch:     "Powai Branch",
		AccountNumber:  "50200012345678",
		IFSCCode:       "HDFC0001234",
	}

	reader, err := pdfGen.GenerateInvoiceHTML(ctx, data)
	if err != nil {
		httpx.WriteServerError(w, "pdf rendering failed", err)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", "inline; filename=\"Invoice-"+invoice.Number+".html\"")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
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
