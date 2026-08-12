package config

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// OAuthCredentials holds the client credentials for a single OIDC provider.
type OAuthCredentials struct {
	ClientID     string
	ClientSecret string
}

// Config holds runtime configuration loaded from the environment.
type Config struct {
	DatabaseURL string
	JWTSecret   string
	JWTIssuer   string
	// JWTAccessTTL is how long an issued access token stays valid. Short on
	// purpose — the refresh token below is what keeps a session alive.
	JWTAccessTTL time.Duration
	// JWTRefreshTTL is how long a refresh token (and so a session) lasts.
	JWTRefreshTTL time.Duration
	NATSURL       string
	GatewayAddr   string
	// Timezone is the session time zone for every database connection; it decides
	// what CURRENT_DATE means, and so what "overdue" and "due today" mean.
	Timezone string
	// WebAppURL is the SPA origin the SSO callback redirects back to.
	WebAppURL string
	// OIDCRedirectBase is the public base URL of the SSO routes, e.g.
	// http://localhost:8080/api/v1/auth/sso — the provider callback URL is
	// "<base>/<provider>/callback".
	OIDCRedirectBase string
	// OAuthCreds holds credentials keyed by provider name ("google", "github").
	// Only providers with a non-empty client id are considered enabled.
	OAuthCreds map[string]OAuthCredentials
}

// knownProviders is the set of OIDC providers whose endpoints the auth module
// knows about. Credentials are read from <UPPER>_CLIENT_ID / _CLIENT_SECRET.
var knownProviders = []string{"google", "github"}

// Load reads configuration from environment variables with sane defaults.
// It first loads a .env file (searched from the working directory upward) if
// present; real environment variables always take precedence over .env values.
func Load() Config {
	loadDotenv()
	return Config{
		DatabaseURL:      getenv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/gocrm?sslmode=disable"),
		JWTSecret:        getenv("JWT_SECRET", "change-me-in-production"),
		JWTIssuer:        getenv("JWT_ISSUER", "go-crm"),
		JWTAccessTTL:     getdur("JWT_ACCESS_TTL", 15*time.Minute),
		JWTRefreshTTL:    getdur("JWT_REFRESH_TTL", 30*24*time.Hour),
		NATSURL:          getenv("NATS_URL", "nats://localhost:4222"),
		GatewayAddr:      gatewayAddr(),
		WebAppURL:        getenv("WEB_APP_URL", "http://localhost:4321"),
		OIDCRedirectBase: getenv("OIDC_REDIRECT_BASE", "http://localhost:8080/api/v1/auth/sso"),
		Timezone:         getenv("APP_TIMEZONE", "UTC"),
		OAuthCreds:       loadOAuthCreds(),
	}
}

// loadDotenv walks up from the working directory looking for a .env file and
// loads it. godotenv.Load does not override variables already set in the
// environment, so real env vars win. Missing .env is not an error (production
// typically injects env vars directly).
func loadDotenv() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for range 6 {
		p := filepath.Join(dir, ".env")
		if _, err := os.Stat(p); err == nil {
			_ = godotenv.Load(p)
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
		}
		dir = parent
	}
}

// gatewayAddr resolves the address the HTTP server listens on.
//
// PORT wins because that is how every PaaS (Railway included) hands a container
// its assigned port, and it is the only port routed to from the edge — a
// GATEWAY_ADDR carried over from a local .env would bind the wrong one and every
// health check would fail. GATEWAY_ADDR stays as the local knob.
func gatewayAddr() string {
	if p := os.Getenv("PORT"); p != "" {
		return ":" + p
	}
	return getenv("GATEWAY_ADDR", ":8080")
}

func loadOAuthCreds() map[string]OAuthCredentials {
	creds := make(map[string]OAuthCredentials)
	for _, name := range knownProviders {
		prefix := strings.ToUpper(name)
		id := os.Getenv(prefix + "_CLIENT_ID")
		if id == "" {
			continue // provider not configured
		}
		creds[name] = OAuthCredentials{
			ClientID:     id,
			ClientSecret: os.Getenv(prefix + "_CLIENT_SECRET"),
		}
	}
	return creds
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getdur(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
