package upstreamauth

import (
	"context"
	"testing"
)

func TestSetStaticHeaderJoinsCallerTransaction(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.insertServer("keyed", "https://keyed.example/mcp", AuthKindStatic)
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.mgr.SetStaticHeader(ctx, tx, id, "X-Api-Key", "k-12345"); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx)
	var set bool
	if err := e.pool.QueryRow(ctx, `SELECT static_header_ciphertext IS NOT NULL FROM upstream_servers WHERE id = $1`, id).Scan(&set); err != nil {
		t.Fatal(err)
	}
	if set {
		t.Fatal("rollback did not undo SetStaticHeader")
	}
}
