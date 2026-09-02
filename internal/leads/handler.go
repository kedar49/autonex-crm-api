package leads

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/go-crm/services/internal/activities"
	"github.com/go-crm/services/internal/integrations"
	"github.com/go-crm/services/pkg/httpx"
	"github.com/go-crm/services/pkg/middleware"
)

// Handler exposes the leads module's HTTP API.
type Handler struct {
	svc *Service
	// pool is here only to write system events to the activity log; the
	// module's own data always goes through svc.
	pool   *pgxpool.Pool
	secret string
	// meetings may be nil; booking is then simply unavailable.
	meetings MeetingBooker
}

// NewHandler wires the leads service to the pgx pool.
// MeetingBooker books a calendar meeting. Narrow interface so the leads module
// depends on the capability rather than the integrations service.
type MeetingBooker interface {
	BookCall(ctx context.Context, userID, title string, startAt time.Time,
		duration time.Duration, leadID, dealID string) (integrations.Meeting, error)
}

// advanceResponse carries the updated lead and, when one was booked, the meeting.
type advanceResponse struct {
	Lead    Lead                  `json:"lead"`
	Meeting *integrations.Meeting `json:"meeting,omitempty"`
}

func NewHandler(pool *pgxpool.Pool, secret string, meetings MeetingBooker) *Handler {
	return &Handler{svc: NewService(pool), pool: pool, secret: secret, meetings: meetings}
}

// Routes returns the leads sub-router, mounted at /api/v1/leads.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequireJWT(h.secret))

	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{id}", h.get)
	r.Put("/{id}", h.update)
	r.Delete("/{id}", h.remove)
	// The list's contextual actions: one step along the lifecycle, optionally
	// rescheduling the follow-up and logging what happened.
	r.Post("/{id}/advance", h.advance)
	// POST, not PATCH: converting creates two new records, it doesn't edit this one.
	r.Post("/{id}/convert", h.convert)
	return r
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	page, err := h.svc.List(r.Context(), middleware.OrgID(r.Context()), q.Get("filter"), limit, offset)
	if err != nil {
		writeErr(w, err, "could not list leads")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
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
	ctx := r.Context()
	lead, err := h.svc.Create(ctx, middleware.OrgID(ctx), in)
	if err != nil {
		writeErr(w, err, "could not create lead")
		return
	}

	// Deals log their creation; leads did not, which left a gap at the very start
	// of every lead's timeline — the one entry that explains where it came from.
	activities.Log(ctx, h.pool, activities.Entry{
		OrgID: middleware.OrgID(ctx), Actor: middleware.UserID(ctx),
		LeadID: lead.ID, ContactID: derefID(lead.ContactID), AccountID: derefID(lead.AccountID),
		Subject: "Lead created", Body: leadLabel(lead),
	})

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

func (h *Handler) advance(w http.ResponseWriter, r *http.Request) {
	var adv Advance
	if !httpx.DecodeJSON(w, r, &adv) {
		return
	}

	ctx := r.Context()
	leadID := chi.URLParam(r, "id")

	// The meeting is booked before the stage moves. If Google refuses — usually
	// because the calendar is not connected — nothing has changed yet, so the
	// user can connect and retry rather than being left with a lead marked
	// "call scheduled" and no call in the diary.
	var meeting *integrations.Meeting
	if adv.MeetingAt != nil && h.meetings != nil {
		existing, err := h.svc.Get(ctx, middleware.OrgID(ctx), leadID)
		if err != nil {
			writeErr(w, err, "could not update that lead")
			return
		}
		minutes := adv.MeetingMinutes
		if minutes <= 0 {
			minutes = 30
		}
		booked, err := h.meetings.BookCall(ctx, middleware.UserID(ctx),
			"Call with "+leadLabel(existing), *adv.MeetingAt,
			time.Duration(minutes)*time.Minute, leadID, "")
		if err != nil {
			if errors.Is(err, integrations.ErrNotConnected) {
				httpx.WriteError(w, http.StatusPreconditionRequired,
					"connect your Google Calendar first, then book the call")
				return
			}
			httpx.WriteServerError(w, "could not create the calendar event", err)
			return
		}
		meeting = &booked
	}

	lead, err := h.svc.AdvanceStage(ctx, middleware.OrgID(ctx), leadID, adv)
	if err != nil {
		writeErr(w, err, "could not update that lead")
		return
	}

	// Two entries on purpose: the stage change is the system's record, and the
	// person's note (if they wrote one) is their own.
	activities.Log(ctx, h.pool, activities.Entry{
		OrgID: middleware.OrgID(ctx), Actor: middleware.UserID(ctx),
		LeadID: lead.ID, ContactID: derefID(lead.ContactID), AccountID: derefID(lead.AccountID),
		Subject: "Lead moved to " + StageLabel(lead.Stage),
	})
	if adv.Note != nil && *adv.Note != "" {
		h.logNote(ctx, lead, *adv.Note)
	}
	if meeting != nil {
		activities.Log(ctx, h.pool, activities.Entry{
			OrgID: middleware.OrgID(ctx), Actor: middleware.UserID(ctx),
			LeadID:  lead.ID,
			Subject: "Google Meet booked for " + meeting.StartAt.Format("2 Jan 15:04"),
			Body:    meeting.MeetLink,
		})
	}

	// The lead is returned under its own key so the booked meeting can travel
	// with it; the client needs the Meet link at the moment it books.
	httpx.WriteJSON(w, http.StatusOK, advanceResponse{Lead: lead, Meeting: meeting})
}

func (h *Handler) convert(w http.ResponseWriter, r *http.Request) {
	var in ConvertInput
	// An empty body is valid — every override is optional.
	if r.ContentLength > 0 && !httpx.DecodeJSON(w, r, &in) {
		return
	}

	ctx := r.Context()
	result, err := h.svc.Convert(ctx, middleware.OrgID(ctx), chi.URLParam(r, "id"), in)
	if err != nil {
		writeErr(w, err, "could not convert lead")
		return
	}

	// Logged against every record the conversion touched, so it shows up on the
	// lead, the new deal and the contact alike.
	activities.Log(ctx, h.pool, activities.Entry{
		OrgID: middleware.OrgID(ctx), Actor: middleware.UserID(ctx),
		LeadID: result.LeadID, DealID: result.DealID,
		ContactID: result.ContactID, AccountID: result.AccountID,
		Subject: "Lead converted to deal",
	})
	// The call notes are history, so they go on the timeline as the person's own
	// entry rather than into a field on the deal.
	if result.CallNotes != "" {
		activities.Log(ctx, h.pool, activities.Entry{
			OrgID: middleware.OrgID(ctx), Actor: middleware.UserID(ctx),
			LeadID: result.LeadID, DealID: result.DealID, ContactID: result.ContactID,
			Kind: "call", Subject: "Call notes", Body: result.CallNotes,
		})
	}

	httpx.WriteJSON(w, http.StatusCreated, result)
}

// logNote records a person's note alongside the stage change that prompted it.
// leadLabel is the one-line description a timeline entry carries: the person,
// and the company when there is one.
func leadLabel(lead Lead) string {
	name := lead.FirstName
	if lead.LastName != nil && *lead.LastName != "" {
		name += " " + *lead.LastName
	}
	switch {
	case lead.AccountName != nil && *lead.AccountName != "":
		return name + " · " + *lead.AccountName
	case lead.Company != nil && *lead.Company != "":
		return name + " · " + *lead.Company
	default:
		return name
	}
}

func (h *Handler) logNote(ctx context.Context, lead Lead, note string) {
	activities.Log(ctx, h.pool, activities.Entry{
		OrgID: middleware.OrgID(ctx), Actor: middleware.UserID(ctx),
		LeadID: lead.ID, ContactID: derefID(lead.ContactID), AccountID: derefID(lead.AccountID),
		Kind: "note", Subject: StageLabel(lead.Stage), Body: note,
	})
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
	case errors.Is(err, ErrRefNotFound):
		httpx.WriteError(w, http.StatusBadRequest,
			"that owner, company or contact is not part of your organization")
	case errors.Is(err, ErrAlreadyConverted):
		httpx.WriteError(w, http.StatusConflict, "this lead has already been converted")
	case IsValidation(err):
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httpx.WriteServerError(w, fallback, err)
	}
}
