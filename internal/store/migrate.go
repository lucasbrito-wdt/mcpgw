package store

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/migrations"
)

// migrationLockID is the transaction-level advisory lock that serializes concurrent migrators.
const migrationLockID int64 = 0x6d6367774d494752 // "mcgwMIGR"

const createMigrationsTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version    text PRIMARY KEY,
	applied_at timestamptz NOT NULL DEFAULT now()
)`

// Migrate applies the embedded migrations that are not yet recorded, one transaction per file,
// and returns the versions applied by this call.
func Migrate(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	return MigrateFS(ctx, pool, migrations.FS)
}

// MigrateFS is Migrate over an arbitrary filesystem of NNNN_name.sql files.
func MigrateFS(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) ([]string, error) {
	files, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(files)

	var applied []string
	for _, file := range files {
		version := strings.TrimSuffix(file, ".sql")
		body, err := fs.ReadFile(fsys, file)
		if err != nil {
			return applied, fmt.Errorf("read %s: %w", file, err)
		}
		done, err := applyOne(ctx, pool, version, string(body))
		if err != nil {
			return applied, fmt.Errorf("migration %s: %w", version, err)
		}
		if done {
			applied = append(applied, version)
		}
	}
	return applied, nil
}

// applyOne runs one migration inside a transaction; it reports false if already applied.
func applyOne(ctx context.Context, pool *pgxpool.Pool, version, body string) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, createMigrationsTable); err != nil {
		return false, err
	}
	var exists bool
	err = tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", version).Scan(&exists)
	if err != nil || exists {
		return false, err
	}
	// No arguments: pgx uses the simple protocol, so a file may hold many statements.
	if _, err := tx.Exec(ctx, body, pgx.QueryExecModeSimpleProtocol); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
