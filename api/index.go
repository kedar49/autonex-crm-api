// Package api exposes the CRM gateway as a single Vercel Serverless Function.
//
// Vercel calls Handler on every inbound request. sync.Once ensures the config,
// database pool, notifier, and Chi router are initialised exactly once per
// cold-start; warm invocations reuse the same objects.
package api

import (
	"context"
	"log"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/Autonex009/autonex-crm-api/internal/accounts"
	"github.com/Autonex009/autonex-crm-api/internal/activities"
	"github.com/Autonex009/autonex-crm-api/internal/auth"
	"github.com/Autonex009/autonex-crm-api/internal/contacts"
	"github.com/Autonex009/autonex-crm-api/internal/dashboard"
	"github.com/Autonex009/autonex-crm-api/internal/deals"
	"github.com/Autonex009/autonex-crm-api/internal/dealtasks"
	"github.com/Autonex009/autonex-crm-api/internal/delivery"
	"github.com/Autonex009/autonex-crm-api/internal/followups"
	"github.com/Autonex009/autonex-crm-api/internal/integrations"
	"github.com/Autonex009/autonex-crm-api/internal/invoices"
	"github.com/Autonex009/autonex-crm-api/internal/leads"
	"github.com/Autonex009/autonex-crm-api/internal/notify"
	"github.com/Autonex009/autonex-crm-api/internal/org"
	"github.com/Autonex009/autonex-crm-api/internal/quotes"
	"github.com/Autonex009/autonex-crm-api/pkg/config"
	"github.com/Autonex009/autonex-crm-api/pkg/database"
	"github.com/Autonex009/autonex-crm-api/pkg/mailer"
	appmw "github.com/Autonex009/autonex-crm-api/pkg/middleware"
)

var (
	router http.Handler
	once   sync.Once
)

// setup builds the Chi router exactly as cmd/gateway/main.go does, but without
// starting an http.Server — Vercel manages the listener.
func setup() {
	cfg := config.Load()

	pool, err := database.NewPool(context.Background(), cfg.DatabaseURL, cfg.Timezone)
	if err != nil {
		log.Fatalf("vercel: db: %v", err)
	}
	// Note: pool.Close() is never called — the serverless instance is killed
	// when idle, which closes everything. This is normal for serverless.

	notifier := notify.New(pool, mailer.New(mailer.Config{
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		User:     cfg.SMTPUser,
		Password: cfg.SMTPPassword,
		From:     cfg.SMTPFrom,
		FromName: cfg.SMTPFromName,
	}), cfg.WebAppURL)

	meetings := integrations.NewService(pool, cfg)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))
	r.Use(appmw.CORS(cfg.WebAppURL))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	r.Mount("/api/v1/auth", auth.NewHandler(pool, cfg).Routes())
	r.Mount("/api/v1/org", org.NewHandler(pool, cfg).Routes())
	r.Mount("/api/v1/integrations", integrations.NewHandler(pool, cfg).Routes())
	r.Mount("/api/v1/leads", leads.NewHandler(pool, cfg.JWTSecret, meetings).Routes())
	r.Mount("/api/v1/deals", deals.NewHandler(pool, cfg.JWTSecret, notifier).Routes())
	r.Mount("/api/v1/delivery", delivery.NewHandler(pool, cfg.JWTSecret).Routes())
	r.Mount("/api/v1/accounts", accounts.NewHandler(pool, cfg.JWTSecret).Routes())
	r.Mount("/api/v1/contacts", contacts.NewHandler(pool, cfg.JWTSecret).Routes())
	r.Mount("/api/v1/quotes", quotes.NewHandler(pool, cfg.JWTSecret).Routes())
	r.Mount("/api/v1/invoices", invoices.NewHandler(pool, cfg.JWTSecret).Routes())
	r.Mount("/api/v1/activities", activities.NewHandler(pool, cfg.JWTSecret).Routes())
	r.Mount("/api/v1/dashboard", dashboard.NewHandler(pool, cfg.JWTSecret).Routes())
	r.Mount("/api/v1/notifications", notify.NewHandler(notifier.Store(), cfg.JWTSecret).Routes())
	r.Mount("/api/v1/actions", followups.NewHandler(pool, cfg.JWTSecret).Routes())
	r.Mount("/api/v1/deal-tasks", dealtasks.NewHandler(pool, cfg.JWTSecret).Routes())

	router = r
}

// Handler is the Vercel entrypoint. Every HTTP request hits this function.
func Handler(w http.ResponseWriter, r *http.Request) {
	once.Do(setup)
	router.ServeHTTP(w, r)
}
