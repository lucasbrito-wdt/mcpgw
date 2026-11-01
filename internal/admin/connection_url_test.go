package admin_test

import (
	"errors"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
)

func TestSetConnectionURL(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.AddServer(ctx, admin.AddServerInput{Slug: "kafka", URL: "https://kafka.example/mcp", AuthKind: "header",
		Header: &admin.HeaderConfig{Name: "Authorization", Prefix: "Basic "}}); err != nil {
		t.Fatal(err)
	}
	enabled := func() bool {
		var on bool
		if err := h.pool.QueryRow(ctx, `SELECT connection_url FROM upstream_servers WHERE slug = 'kafka'`).Scan(&on); err != nil {
			t.Fatal(err)
		}
		return on
	}
	if err := h.svc.SetConnectionURL(ctx, "kafka", true); err != nil || !enabled() {
		t.Fatalf("enable: %v", err)
	}
	ev := h.events[len(h.events)-1]
	if ev.Action != "server.set_connection_url" || ev.Target != "kafka" || ev.Details["enabled"] != true {
		t.Fatalf("audit = %+v", ev)
	}
	if err := h.svc.SetConnectionURL(ctx, "kafka", false); err != nil || enabled() {
		t.Fatalf("disable: %v", err)
	}
	if _, err := h.svc.AddServer(ctx, portainerInput()); err != nil {
		t.Fatal(err)
	}
	var ie *admin.InvalidError
	if err := h.svc.SetConnectionURL(ctx, "portainer", true); !errors.As(err, &ie) || ie.Field != "connection_url" {
		t.Fatalf("stdio accepted: %v", err)
	}
	if err := h.svc.SetConnectionURL(ctx, "missing", true); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("missing server: %v", err)
	}
}
