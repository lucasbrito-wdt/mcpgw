package catalog_test

import (
	"context"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/catalog"
	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

func TestApproveJoinsCallerTransaction(t *testing.T) {
	pool := storetest.New(t)
	ctx := context.Background()
	srv := newServer(t, pool, "every")
	lister := &fakeLister{tools: []upstream.Tool{tool("create_item", "Create", upstream.Annotations{})}}
	mustSync(t, pool, lister, srv)
	lister.tools[0] = tool("create_item", "Create v2", upstream.Annotations{})
	mustSync(t, pool, lister, srv) // changed definition -> pending approval

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := catalog.Approve(ctx, tx, srv.ID, "create_item")
	if err != nil || len(approved) != 1 {
		t.Fatalf("approve in tx: %v %v", approved, err)
	}
	_ = tx.Rollback(ctx)

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM tools WHERE server_id = $1 AND name = 'create_item'`, srv.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != catalog.StatusPendingApproval {
		t.Fatalf("rollback did not undo approve: %s", status)
	}
}
