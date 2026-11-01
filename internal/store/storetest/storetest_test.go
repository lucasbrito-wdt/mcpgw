package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestIsStale(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cases := map[string]bool{
		testDatabaseName(now):                                false,
		testDatabaseName(now.Add(-staleAfter + time.Minute)): false,
		testDatabaseName(now.Add(-staleAfter - time.Minute)): true,
		"mcpgw_test_48f3645ea441":                            false, // legacy name without timestamp
		"mcpgw_test_abc_def":                                 false,
		"mcpgw":                                              false,
		"other_1_x":                                          false,
	}
	for name, want := range cases {
		if got := isStale(name, now); got != want {
			t.Errorf("isStale(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestSweepDropsOnlyStaleDatabases(t *testing.T) {
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

	now := time.Now()
	stale := testDatabaseName(now.Add(-staleAfter - time.Hour))
	fresh := testDatabaseName(now)
	for _, name := range []string{stale, fresh} {
		if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		t.Cleanup(func() { _ = dropDatabase(baseURL, name) })
	}

	sweepStale(ctx, admin, now)

	exists := func(name string) bool {
		var ok bool
		if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&ok); err != nil {
			t.Fatalf("lookup %s: %v", name, err)
		}
		return ok
	}
	if exists(stale) {
		t.Errorf("stale database %s survived the sweep", stale)
	}
	if !exists(fresh) {
		t.Errorf("fresh database %s was dropped by the sweep", fresh)
	}
}
