// Package storetest provides isolated throwaway Postgres databases for tests.
package storetest

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/internal/store"
)

const (
	databaseURLEnv = "MCPGW_DATABASE_URL"
	dbPrefix       = "mcpgw_test_"
	// staleAfter must exceed any test binary's lifetime, so a sweep never drops a live database.
	staleAfter = 2 * time.Hour
)

var sweepOnce sync.Once

// New returns a pool on a fresh throwaway database with all migrations applied.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool := NewEmpty(t)
	if _, err := store.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	return pool
}

// NewEmpty returns a pool on a fresh throwaway database with no schema.
// The database is dropped when the test finishes. The test is skipped when
// MCPGW_DATABASE_URL is neither set nor present in the repo-root .env file.
func NewEmpty(t testing.TB) *pgxpool.Pool {
	t.Helper()
	baseURL := lookupDatabaseURL()
	if baseURL == "" {
		t.Skipf("%s not set", databaseURLEnv)
	}
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer admin.Close(ctx)

	// t.Cleanup never runs when the test process is killed, so each process sweeps leftovers once.
	sweepOnce.Do(func() { sweepStale(ctx, admin, time.Now()) })

	name := testDatabaseName(time.Now())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create test database: %v", err)
	}

	pool, err := store.NewPool(ctx, withDatabase(t, baseURL, name))
	if err != nil {
		dropDatabase(baseURL, name)
		t.Fatalf("open test pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		if err := dropDatabase(baseURL, name); err != nil {
			t.Logf("drop test database %s: %v", name, err)
		}
	})
	return pool
}

func dropDatabase(baseURL, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		return err
	}
	defer admin.Close(ctx)
	_, err = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	return err
}

// testDatabaseName embeds the creation time so a later run can tell leftovers from live databases.
func testDatabaseName(now time.Time) string {
	return dbPrefix + strconv.FormatInt(now.Unix(), 10) + "_" + randomHex(6)
}

// isStale reports whether name is a test database created more than staleAfter before now.
// Names without a timestamp are not ours to judge and are left alone.
func isStale(name string, now time.Time) bool {
	rest, ok := strings.CutPrefix(name, dbPrefix)
	if !ok {
		return false
	}
	stamp, _, ok := strings.Cut(rest, "_")
	if !ok {
		return false
	}
	sec, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return false
	}
	return now.Sub(time.Unix(sec, 0)) > staleAfter
}

// sweepStale drops test databases left behind by killed test processes. Best effort.
func sweepStale(ctx context.Context, admin *pgx.Conn, now time.Time) {
	rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE $1", dbPrefix+"%")
	if err != nil {
		return
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return
	}
	for _, name := range names {
		if isStale(name, now) {
			_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		}
	}
}

func withDatabase(t testing.TB, baseURL, name string) string {
	t.Helper()
	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse %s: %v", databaseURLEnv, err)
	}
	u.Path = "/" + name
	return u.String()
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// lookupDatabaseURL reads the env var, falling back to the .env file at the module root.
func lookupDatabaseURL() string {
	if v := os.Getenv(databaseURLEnv); v != "" {
		return v
	}
	root, ok := moduleRoot()
	if !ok {
		return ""
	}
	f, err := os.Open(filepath.Join(root, ".env"))
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if v, ok := strings.CutPrefix(strings.TrimSpace(scanner.Text()), databaseURLEnv+"="); ok {
			return strings.Trim(v, `"'`)
		}
	}
	return ""
}

func moduleRoot() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
