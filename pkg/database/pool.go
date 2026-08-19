package database

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
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
func NewPool(ctx context.Context, dsn, tz string) (*pgxpool.Pool, error) {
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

	// Pin the session time zone for every pooled connection.
	//
	// This is not cosmetic. "Overdue" and "due today" are `CURRENT_DATE`
	// comparisons, and CURRENT_DATE is the *session's* date — so a UTC session
	// serving a UTC+05:30 team reports yesterday's date between midnight and
	// 05:30 local, and every follow-up silently shifts by a day during that
	// window. Setting it here fixes every such query at once, rather than
	// threading a time zone through each one.
	// Sent as an explicit SET rather than a startup RuntimeParam: the connection
	// pooler in front of Postgres does not forward startup parameters, so the
	// session silently stayed on UTC. This runs once per physical connection,
	// which the pool opens rarely.
	if tz != "" {
		// Rejected at boot rather than per query: an unknown name would otherwise
		// leave every connection on UTC, and the only symptom would be follow-up
		// dates quietly off by a day.
		if _, err := time.LoadLocation(tz); err != nil {
			return nil, fmt.Errorf("APP_TIMEZONE %q is not a known IANA time zone: %w", tz, err)
		}
		cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			_, err := conn.Exec(ctx, "SET TIME ZONE "+quoteLiteral(tz))
			return err
		}
	}

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

// quoteLiteral renders s as a single-quoted SQL literal.
//
// SET TIME ZONE takes no bind parameters, so the value has to be inlined. The
// name is validated against the tzdata before it reaches here, but doubling any
// embedded quote keeps a hand-edited config value from ending the literal early.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
