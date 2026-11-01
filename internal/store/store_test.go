package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/store"
	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
)

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := storetest.NewEmpty(t)

	first, err := store.Migrate(ctx, pool)
	if err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("first migrate applied nothing")
	}
	second, err := store.Migrate(ctx, pool)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("second migrate should be a no-op, applied %v", second)
	}
}

func TestSchemaConstraints(t *testing.T) {
	ctx := context.Background()
	pool := storetest.New(t)

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (email, password_hash) VALUES ('a@example.com', 'h')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (email, password_hash) VALUES ('a@example.com', 'h')`); err == nil {
		t.Fatal("duplicate email should fail")
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO upstream_servers (slug, url, auth_kind) VALUES ('s', 'http://u', 'none')`); err != nil {
		t.Fatalf("insert server: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO tools (server_id, name, schema_hash, trust_level)
		SELECT id, 't', 'h', 'admin' FROM upstream_servers`); err == nil {
		t.Fatal("invalid trust level should fail")
	}
}

func TestAuditPartitions(t *testing.T) {
	ctx := context.Background()
	pool := storetest.New(t)

	if _, err := pool.Exec(ctx, `SELECT create_audit_partition('2031-03-15')`); err != nil {
		t.Fatalf("create partition: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT create_audit_partition('2031-03-01')`); err != nil {
		t.Fatalf("create partition must be idempotent: %v", err)
	}
	_, err := pool.Exec(ctx, `INSERT INTO audit_events (ts, user_id, client_id, server_slug, tool_name, decision)
		VALUES ('2031-03-20T00:00:00Z', uuidv7(), 'c', 's', 't', 'allow'),
		       ('1999-01-01T00:00:00Z', uuidv7(), 'c', 's', 't', 'deny')`)
	if err != nil {
		t.Fatalf("insert audit events: %v", err)
	}
	var inMonth, inDefault int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events_2031_03`).Scan(&inMonth); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events_default`).Scan(&inDefault); err != nil {
		t.Fatal(err)
	}
	if inMonth != 1 || inDefault != 1 {
		t.Fatalf("routing wrong: month=%d default=%d", inMonth, inDefault)
	}
}

func TestPolicyChangedNotify(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := storetest.New(t)

	var userID, serverID string
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash) VALUES ('n@example.com', 'h') RETURNING id::text`).Scan(&userID)
	if err != nil {
		t.Fatal(err)
	}
	err = pool.QueryRow(ctx,
		`INSERT INTO upstream_servers (slug, url, auth_kind) VALUES ('n', 'http://u', 'none') RETURNING id::text`).Scan(&serverID)
	if err != nil {
		t.Fatal(err)
	}

	listener, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Release()
	if _, err := listener.Exec(ctx, "LISTEN policy_changed"); err != nil {
		t.Fatal(err)
	}

	statements := []string{
		`INSERT INTO trust_ceilings (user_id, server_id, max_level) VALUES ('` + userID + `', '` + serverID + `', 'write')`,
		`INSERT INTO tools (server_id, name, schema_hash, trust_level) VALUES ('` + serverID + `', 't', 'h', 'read')`,
	}
	for _, sql := range statements {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		n, err := listener.Conn().WaitForNotification(ctx)
		if err != nil {
			t.Fatalf("waiting for notification after %q: %v", sql, err)
		}
		if n.Channel != "policy_changed" || n.Payload == "" {
			t.Fatalf("unexpected notification: %+v", n)
		}
	}

	// A metadata-only update must stay silent, a trust level change must notify.
	if _, err := pool.Exec(ctx, `UPDATE tools SET description = 'metadata only'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE tools SET trust_level = 'write'`); err != nil {
		t.Fatal(err)
	}
	n, err := listener.Conn().WaitForNotification(ctx)
	if err != nil {
		t.Fatalf("waiting for trust level notification: %v", err)
	}
	if !strings.Contains(n.Payload, `"table": "tools"`) {
		t.Fatalf("expected the tools trigger to be first after the update, got %s", n.Payload)
	}
}
