package database

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool tuning for a connection that goes through a proxy (Supabase's Supavisor)
// over the public internet.
//
// The defaults were wrong here in a way that only shows up after a pause: pgx
// keeps idle connections indefinitely, but the pooler — and any NAT or WiFi
// router in between — drops idle TCP after a minute or so. The dead socket stays
// in the pool and the next query fails *instantly* (a write to a closed socket
// doesn't wait for a round-trip), so a page that worked a minute ago returns a
// 500 in under a millisecond and then works again on retry.
//
// maxConnIdleTime is therefore deliberately shorter than any plausible upstream
// idle timeout: retire our own connections before someone else does it for us.
const (
	maxConns          = 8
	maxConnIdleTime   = 30 * time.Second
	maxConnLifetime   = 30 * time.Minute
	lifetimeJitter    = 5 * time.Minute
	healthCheckPeriod = 15 * time.Second
	connectTimeout    = 10 * time.Second
)

// NewPool creates a pgx connection pool and verifies connectivity.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}

	cfg.MaxConns = maxConns
	// No floor: a warm idle connection is exactly the thing that goes stale, and
	// reconnecting costs one round-trip on the next request.
	cfg.MinConns = 0
	cfg.MaxConnIdleTime = maxConnIdleTime
	// Jitter so the whole pool doesn't turn over at the same instant.
	cfg.MaxConnLifetime = maxConnLifetime
	cfg.MaxConnLifetimeJitter = lifetimeJitter
	// The reaper enforces the two limits above; it has to run several times per
	// idle window to be useful.
	cfg.HealthCheckPeriod = healthCheckPeriod
	cfg.ConnConfig.ConnectTimeout = connectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
