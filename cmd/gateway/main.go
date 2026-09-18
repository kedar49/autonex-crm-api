package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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

// Gateway is the HTTP API edge. Domain modules register their routes here.
func main() {
	cfg := config.Load()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := database.NewPool(ctx, cfg.DatabaseURL, cfg.Timezone)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	// Notification email. With no SMTP host configured this is a no-op sender,
	// so the gateway behaves identically minus the mail.
	notifier := notify.New(pool, mailer.New(mailer.Config{
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		User:     cfg.SMTPUser,
		Password: cfg.SMTPPassword,
		From:     cfg.SMTPFrom,
		FromName: cfg.SMTPFromName,
	}), cfg.WebAppURL)

	// Third-party connections (Google Calendar) and the meeting booking they enable.
	meetings := integrations.NewService(pool, cfg)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	// gzip the JSON list payloads — they compress roughly 5-10x, which is the
	// single largest win on a mobile connection. Already-compressed content types
	// are skipped by the middleware.
	r.Use(middleware.Compress(5))
	// Allow the browser SPA (cfg.WebAppURL) to call the API cross-origin.
	r.Use(appmw.CORS(cfg.WebAppURL))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Domain modules register their sub-routers here.
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

	srv := &http.Server{
		Addr:              cfg.GatewayAddr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("gateway listening on %s", cfg.GatewayAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
}
