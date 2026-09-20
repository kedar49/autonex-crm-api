// Package api exposes the CRM gateway as a single Vercel Serverless Function.
//
// Vercel calls Handler on every inbound request. sync.Once ensures the config,
// database pool, and Chi router are initialised exactly once per cold-start;
// warm invocations reuse the same objects.
//
// This file deliberately imports only pkg/* packages. Vercel compiles api/ as
// package path "api/api", which sits outside the Go module tree and is therefore
// blocked by Go's internal-package visibility rule from importing internal/*.
// The actual wiring lives in pkg/server, which IS inside the module.
package api

import (
	"context"
	"log"
	"net/http"
	"sync"

	"github.com/Autonex009/autonex-crm-api/pkg/config"
	"github.com/Autonex009/autonex-crm-api/pkg/database"
	"github.com/Autonex009/autonex-crm-api/pkg/server"
)

var (
	router http.Handler
	once   sync.Once
)

// setup builds the Chi router on the first cold-start request.
func setup() {
	cfg := config.Load()

	pool, err := database.NewPool(context.Background(), cfg.DatabaseURL, cfg.Timezone)
	if err != nil {
		log.Fatalf("vercel: db: %v", err)
	}
	// Note: pool.Close() is never called — the serverless instance is killed
	// when idle, which closes everything. This is normal for serverless.

	router = server.NewRouter(cfg, pool)
}

// Handler is the Vercel entrypoint. Every HTTP request hits this function.
func Handler(w http.ResponseWriter, r *http.Request) {
	once.Do(setup)
	router.ServeHTTP(w, r)
}
