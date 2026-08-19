package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/go-crm/services/pkg/config"
	"github.com/go-crm/services/pkg/httpx"
	"github.com/go-crm/services/pkg/middleware"
)

const ssoStateCookie = "sso_state"

// Handler exposes the auth module's HTTP API.
type Handler struct {
	svc *Service
	cfg config.Config
}

// NewHandler wires the auth service to the pgx pool and config.
func NewHandler(pool *pgxpool.Pool, cfg config.Config) *Handler {
	return &Handler{svc: newService(pool, cfg), cfg: cfg}
}

// Routes returns the auth sub-router, mounted at /api/v1/auth by the gateway.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/register", h.register)
	r.Post("/login", h.login)
	// Both read the refresh cookie rather than an Authorization header: the
	// caller's access token is expected to be expired by the time it refreshes.
	r.Post("/refresh", h.refresh)
	r.Post("/logout", h.logout)
	r.Get("/sso/{provider}", h.ssoStart)
	r.Get("/sso/{provider}/callback", h.ssoCallback)

	r.Group(func(pr chi.Router) {
		pr.Use(middleware.RequireJWT(h.cfg.JWTSecret))
		pr.Get("/me", h.me)
	})
	return r
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeCredentials(w, r)
	if !ok {
		return
	}
	session, err := h.svc.Register(r.Context(), in.Email, in.Password)
	if errors.Is(err, ErrEmailTaken) {
		httpx.WriteError(w, http.StatusConflict, "email already registered")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "could not create account")
		return
	}
	h.writeSession(w, http.StatusCreated, session)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeCredentials(w, r)
	if !ok {
		return
	}
	session, err := h.svc.Login(r.Context(), in.Email, in.Password)
	if errors.Is(err, ErrInvalidCredentials) {
		httpx.WriteError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "login failed")
		return
	}
	h.writeSession(w, http.StatusOK, session)
}

func (h *Handler) ssoStart(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")

	state, err := randomState()
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	authURL, err := h.svc.AuthCodeURL(provider, state)
	if errors.Is(err, ErrUnknownProvider) {
		httpx.WriteError(w, http.StatusNotFound, "unknown or unconfigured provider")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     ssoStateCookie,
		Value:    state,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		Secure:   h.secureCookies(),
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (h *Handler) ssoCallback(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")

	// CSRF: the state echoed by the provider must match the cookie we set.
	cookie, err := r.Cookie(ssoStateCookie)
	if err != nil || cookie.Value == "" || cookie.Value != r.URL.Query().Get("state") {
		httpx.WriteError(w, http.StatusBadRequest, "invalid state")
		return
	}
	// Consume the state cookie.
	http.SetCookie(w, &http.Cookie{Name: ssoStateCookie, Path: "/", MaxAge: -1})

	code := r.URL.Query().Get("code")
	if code == "" {
		httpx.WriteError(w, http.StatusBadRequest, "missing authorization code")
		return
	}

	session, err := h.svc.CompleteSSO(r.Context(), provider, code)
	switch {
	case errors.Is(err, ErrUnknownProvider):
		httpx.WriteError(w, http.StatusNotFound, "unknown or unconfigured provider")
		return
	case errors.Is(err, ErrEmailTaken):
		httpx.WriteError(w, http.StatusConflict, "that email is already registered with a different login method")
		return
	case err != nil:
		httpx.WriteError(w, http.StatusUnauthorized, "sso login failed")
		return
	}

	// The refresh cookie is set on this top-level navigation, so the session
	// survives a reload; the access token still rides the URL fragment, which is
	// never sent to a server or written to a log.
	SetRefreshCookie(w, h.cfg, session.RefreshToken, session.RefreshExpiresAt)
	redirect := strings.TrimRight(h.cfg.WebAppURL, "/") + "/app#token=" +
		url.QueryEscape(session.AccessToken)
	http.Redirect(w, r, redirect, http.StatusFound)
}

// refresh rotates the session. The old refresh token is spent by this call, so a
// client must use the value returned here from now on.
func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(RefreshCookieName)
	if err != nil || cookie.Value == "" {
		httpx.WriteError(w, http.StatusUnauthorized, "no session")
		return
	}

	session, err := h.svc.Refresh(r.Context(), cookie.Value)
	if errors.Is(err, ErrInvalidRefresh) {
		// Clear the dead cookie so the browser stops sending it.
		ClearRefreshCookie(w, h.cfg)
		httpx.WriteError(w, http.StatusUnauthorized, "session expired")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "could not refresh the session")
		return
	}
	h.writeSession(w, http.StatusOK, session)
}

// logout revokes the refresh token server-side, so the session is dead even if a
// copy of the cookie survives somewhere.
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(RefreshCookieName); err == nil {
		if err := h.svc.Logout(r.Context(), cookie.Value); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "could not sign out")
			return
		}
	}
	ClearRefreshCookie(w, h.cfg)
	w.WriteHeader(http.StatusNoContent)
}

// writeSession sets the refresh cookie and returns the access token in the body.
// The split is deliberate: the refresh token is the long-lived credential and
// stays out of reach of script, while the access token is short-lived and held in
// memory by the SPA.
func (h *Handler) writeSession(w http.ResponseWriter, status int, session Session) {
	SetRefreshCookie(w, h.cfg, session.RefreshToken, session.RefreshExpiresAt)
	httpx.WriteJSON(w, status, authResponse{Token: session.AccessToken, User: session.User})
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"userId": middleware.UserID(r.Context()),
		"orgId":  middleware.OrgID(r.Context()),
	})
}

func (h *Handler) secureCookies() bool {
	return strings.HasPrefix(h.cfg.OIDCRedirectBase, "https://")
}

// --- helpers ---

func decodeCredentials(w http.ResponseWriter, r *http.Request) (credentials, bool) {
	var in credentials
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return credentials{}, false
	}
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	if in.Email == "" || !strings.Contains(in.Email, "@") {
		httpx.WriteError(w, http.StatusBadRequest, "a valid email is required")
		return credentials{}, false
	}
	if len(in.Password) < 8 {
		httpx.WriteError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return credentials{}, false
	}
	return in, true
}

func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
