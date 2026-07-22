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
	tok, user, err := h.svc.Register(r.Context(), in.Email, in.Password)
	if errors.Is(err, ErrEmailTaken) {
		writeError(w, http.StatusConflict, "email already registered")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create account")
		return
	}
	writeJSON(w, http.StatusCreated, authResponse{Token: tok, User: user})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeCredentials(w, r)
	if !ok {
		return
	}
	tok, user, err := h.svc.Login(r.Context(), in.Email, in.Password)
	if errors.Is(err, ErrInvalidCredentials) {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "login failed")
		return
	}
	writeJSON(w, http.StatusOK, authResponse{Token: tok, User: user})
}

func (h *Handler) ssoStart(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")

	state, err := randomState()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	authURL, err := h.svc.AuthCodeURL(provider, state)
	if errors.Is(err, ErrUnknownProvider) {
		writeError(w, http.StatusNotFound, "unknown or unconfigured provider")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
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
		writeError(w, http.StatusBadRequest, "invalid state")
		return
	}
	// Consume the state cookie.
	http.SetCookie(w, &http.Cookie{Name: ssoStateCookie, Path: "/", MaxAge: -1})

	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "missing authorization code")
		return
	}

	token, err := h.svc.CompleteSSO(r.Context(), provider, code)
	switch {
	case errors.Is(err, ErrUnknownProvider):
		writeError(w, http.StatusNotFound, "unknown or unconfigured provider")
		return
	case errors.Is(err, ErrEmailTaken):
		writeError(w, http.StatusConflict, "that email is already registered with a different login method")
		return
	case err != nil:
		writeError(w, http.StatusUnauthorized, "sso login failed")
		return
	}

	// Hand the token to the SPA via the URL fragment (never sent to servers/logs).
	redirect := strings.TrimRight(h.cfg.WebAppURL, "/") + "/app#token=" + url.QueryEscape(token)
	http.Redirect(w, r, redirect, http.StatusFound)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"userId": middleware.UserID(r.Context())})
}

func (h *Handler) secureCookies() bool {
	return strings.HasPrefix(h.cfg.OIDCRedirectBase, "https://")
}

// --- helpers ---

func decodeCredentials(w http.ResponseWriter, r *http.Request) (credentials, bool) {
	var in credentials
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return credentials{}, false
	}
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	if in.Email == "" || !strings.Contains(in.Email, "@") {
		writeError(w, http.StatusBadRequest, "a valid email is required")
		return credentials{}, false
	}
	if len(in.Password) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
