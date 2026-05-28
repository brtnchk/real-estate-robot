// Package db is the hand-written Postgres layer. It owns the pgxpool
// lifecycle. The actual typed queries live in the sqlc sub-package
// (internal/db/sqlc) and are generated from internal/db/queries/*.sql.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect parses the URL, applies pool defaults, opens the pool, and pings
// the server to verify connectivity. pgxpool.NewWithConfig is lazy — it
// does not actually open a TCP connection until something asks for one —
// so a missing/down Postgres would not be detected here without the Ping.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse pg url: %w", err)
	}

	// Pool defaults. These are conservative; revisit when worker concurrency
	// grows. MaxConns is the upper bound — pgxpool will block when reached,
	// not error.
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.MaxConnLifetime = 1 * time.Hour
	// HealthCheckPeriod is how often pgxpool reaps stale connections. Default
	// 1 minute is fine.

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping pg: %w", err)
	}

	return pool, nil
}