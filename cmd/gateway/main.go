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

	"github.com/go-crm/services/internal/accounts"
	"github.com/go-crm/services/internal/activities"
	"github.com/go-crm/services/internal/auth"
	"github.com/go-crm/services/internal/contacts"
	"github.com/go-crm/services/internal/dashboard"
	"github.com/go-crm/services/internal/deals"
	"github.com/go-crm/services/internal/integrations"
	"github.com/go-crm/services/internal/invoices"
	"github.com/go-crm/services/internal/leads"
	"github.com/go-crm/services/internal/notify"
	"github.com/go-crm/services/internal/org"
	"github.com/go-crm/services/internal/quotes"
	"github.com/go-crm/services/pkg/config"
	"github.com/go-crm/services/pkg/database"
	"github.com/go-crm/services/pkg/mailer"
	appmw "github.com/go-crm/services/pkg/middleware"
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
	r.Mount("/api/v1/accounts", accounts.NewHandler(pool, cfg.JWTSecret).Routes())
	r.Mount("/api/v1/contacts", contacts.NewHandler(pool, cfg.JWTSecret).Routes())
	r.Mount("/api/v1/quotes", quotes.NewHandler(pool, cfg.JWTSecret).Routes())
	r.Mount("/api/v1/invoices", invoices.NewHandler(pool, cfg.JWTSecret).Routes())
	r.Mount("/api/v1/activities", activities.NewHandler(pool, cfg.JWTSecret).Routes())
	r.Mount("/api/v1/dashboard", dashboard.NewHandler(pool, cfg.JWTSecret).Routes())

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
