package integrations

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/go-crm/services/pkg/config"
	"github.com/go-crm/services/pkg/httpx"
	"github.com/go-crm/services/pkg/middleware"
)

const stateCookie = "integration_state"

// Handler exposes the connect/disconnect API for third-party accounts.
type Handler struct {
	svc *Service
	cfg config.Config
}

func NewHandler(pool *pgxpool.Pool, cfg config.Config) *Handler {
	return &Handler{svc: NewService(pool, cfg), cfg: cfg}
}

// Routes returns the sub-router, mounted at /api/v1/integrations.
//
// The callback is deliberately outside the JWT group: it is a redirect from
// Google, arriving as a top-level navigation with no Authorization header. It
// authenticates instead through the signed state it echoes back.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()

	r.Get("/google/callback", h.googleCallback)

	r.Group(func(pr chi.Router) {
		pr.Use(middleware.RequireJWT(h.cfg.JWTSecret))
		pr.Get("/", h.list)
		pr.Post("/google/connect", h.googleConnect)
		pr.Delete("/google", h.googleDisconnect)
	})
	return r
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.List(r.Context(), middleware.UserID(r.Context()))
	if err != nil {
		httpx.WriteServerError(w, "could not load connections", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, items)
}

// googleConnect returns the consent URL rather than redirecting.
//
// The SPA calls this with its bearer token and then navigates itself. A redirect
// here would not work: the browser cannot attach an Authorization header to a
// top-level navigation, so the endpoint could not know who was connecting.
func (h *Handler) googleConnect(w http.ResponseWriter, r *http.Request) {
	oc, err := googleConfig(h.cfg)
	if err != nil {
		httpx.WriteError(w, http.StatusNotImplemented, err.Error())
		return
	}

	nonce, err := randomNonce()
	if err != nil {
		httpx.WriteServerError(w, "could not start the connection", err)
		return
	}
	state := signState(h.cfg.JWTSecret, middleware.UserID(r.Context()), nonce)

	http.SetCookie(w, &http.Cookie{
		Name:     stateCookie,
		Value:    nonce,
		Path:     "/",
		MaxAge:   int(stateTTL.Seconds()),
		HttpOnly: true,
		Secure:   strings.HasPrefix(h.cfg.IntegrationsRedirectBase, "https://"),
		// Lax, not Strict: the cookie has to survive Google redirecting the
		// browser back to us, which is a cross-site navigation.
		SameSite: http.SameSiteLaxMode,
	})

	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"authUrl": authCodeURL(oc, state),
	})
}

// googleCallback completes the consent and stores the tokens.
func (h *Handler) googleCallback(w http.ResponseWriter, r *http.Request) {
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		// The user pressed "Cancel" on Google's screen; that is not a failure.
		h.redirect(w, r, "google", errMsg)
		return
	}

	userID, nonce, err := parseState(h.cfg.JWTSecret, r.URL.Query().Get("state"))
	if err != nil {
		h.redirect(w, r, "google", err.Error())
		return
	}
	cookie, err := r.Cookie(stateCookie)
	if err != nil || cookie.Value == "" || cookie.Value != nonce {
		h.redirect(w, r, "google", "this connection could not be verified; please try again")
		return
	}
	// Consume the nonce so the callback cannot be replayed.
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Path: "/", MaxAge: -1})

	code := r.URL.Query().Get("code")
	if code == "" {
		h.redirect(w, r, "google", "google returned no authorization code")
		return
	}

	if err := h.svc.CompleteGoogle(r.Context(), userID, code); err != nil {
		log.Printf("integrations: google connect failed for user %s: %v", userID, err)
		h.redirect(w, r, "google", "could not connect that Google account")
		return
	}
	h.redirect(w, r, "google", "")
}

func (h *Handler) googleDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Disconnect(r.Context(), middleware.UserID(r.Context()), "google"); err != nil {
		httpx.WriteServerError(w, "could not disconnect", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// redirect sends the browser back into the SPA, reporting the outcome in the
// query string so the page can show it.
func (h *Handler) redirect(w http.ResponseWriter, r *http.Request, provider, errMsg string) {
	base := strings.TrimRight(h.cfg.WebAppURL, "/") + "/app/team"
	q := url.Values{}
	if errMsg != "" {
		q.Set("connectError", errMsg)
	} else {
		q.Set("connected", provider)
	}
	http.Redirect(w, r, base+"?"+q.Encode(), http.StatusFound)
}

// writeErr maps the module's errors onto status codes.
func writeErr(w http.ResponseWriter, err error, fallback string) {
	if errors.Is(err, ErrNotConnected) {
		httpx.WriteError(w, http.StatusPreconditionRequired,
			"connect your Google account first")
		return
	}
	httpx.WriteServerError(w, fallback, err)
}
