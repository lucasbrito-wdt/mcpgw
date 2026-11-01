// Package store owns the Postgres connection pool and schema migrations.
package store

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// defaultMaxConns replaces pgx's max(4, NumCPU): every execute writes two audit rows and the
// sandbox runs 2x GOMAXPROCS scripts at once, so 12 connections queued under load. Kept modest
// because a dev Postgres is often shared (max_connections 100). A
// pool_max_conns in the database URL still wins.
const defaultMaxConns = 16

// NewPool opens a pgx pool for databaseURL and verifies connectivity.
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if !setsMaxConns(databaseURL) {
		cfg.MaxConns = defaultMaxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// setsMaxConns reports whether the URL or keyword/value DSN sets pool_max_conns.
func setsMaxConns(databaseURL string) bool {
	if u, err := url.Parse(databaseURL); err == nil && u.Scheme != "" {
		return u.Query().Has("pool_max_conns")
	}
	return strings.Contains(databaseURL, "pool_max_conns")
}
