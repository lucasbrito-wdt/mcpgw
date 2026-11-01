package upstreamauth

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// The policy engine listens on policy_changed; upstream_servers changes must notify with the
// shared payload shape ({"table","op","user_id","server_id"}).
func TestUpstreamServersNotifyPolicyChanged(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	conn, err := e.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN policy_changed"); err != nil {
		t.Fatal(err)
	}

	type payload struct {
		Table    string  `json:"table"`
		Op       string  `json:"op"`
		UserID   *string `json:"user_id"`
		ServerID string  `json:"server_id"`
	}
	var expect func(op string)
	expect = func(op string) {
		t.Helper()
		wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		n, err := conn.Conn().WaitForNotification(wctx)
		if err != nil {
			t.Fatalf("%s: no notification: %v", op, err)
		}
		var p payload
		if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
			t.Fatal(err)
		}
		if p.Table == "trust_ceilings" { // cascade from the server DELETE (newEnv grants ceilings)
			expect(op)
			return
		}
		if p.Table != "upstream_servers" || p.Op != op || p.UserID != nil || p.ServerID != e.serverID {
			t.Fatalf("payload %s, want op %s server %s", n.Payload, op, e.serverID)
		}
	}
	expectNone := func(what string) {
		t.Helper()
		wctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		defer cancel()
		if n, err := conn.Conn().WaitForNotification(wctx); err == nil {
			t.Fatalf("%s must not notify, got %s", what, n.Payload)
		}
	}

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := e.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	// The insert from newEnv happened before LISTEN; drain nothing, then exercise each path.
	exec(`UPDATE upstream_servers SET status = 'disabled' WHERE id = $1::uuid`, e.serverID)
	expect("UPDATE")
	exec(`UPDATE upstream_servers SET slug = 'beta' WHERE id = $1::uuid`, e.serverID)
	expect("UPDATE")
	exec(`UPDATE upstream_servers SET url = 'https://moved.example/mcp' WHERE id = $1::uuid`, e.serverID)
	expectNone("a url change")
	exec(`DELETE FROM upstream_servers WHERE id = $1::uuid`, e.serverID)
	expect("DELETE")

	e.serverID = e.insertServer("gamma", "https://g.example/mcp", AuthKindNone)
	expect("INSERT")
}

func TestReservedSlugsRejected(t *testing.T) {
	e := newEnv(t)
	for _, slug := range []string{"callback", "success"} {
		_, err := e.pool.Exec(context.Background(),
			`INSERT INTO upstream_servers (slug, url, auth_kind) VALUES ($1, 'https://x.example', 'oauth')`, slug)
		if err == nil {
			t.Errorf("slug %q accepted", slug)
		}
	}
}
