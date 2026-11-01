package codemode

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/audit"
	"github.com/lucasbrito-wdt/mcpgw/internal/catalog"
	"github.com/lucasbrito-wdt/mcpgw/internal/policy"
	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
)

// inconsistentSnapshot has a tool whose raw name equals the identifier a different tool has in
// the map: the state F3 must refuse instead of picking one of the two.
func inconsistentSnapshot() *snapshot {
	return &snapshot{
		loaded:      time.Now(),
		byQualified: map[string]catalog.ToolDef{"acme.get_item": {Server: "acme", Name: "get_item"}},
		servers:     map[string]serverInfo{"acme": {id: "1"}},
		idents:      map[string]*catalog.IdentMap{"acme": catalog.NewIdentMap([]string{"get-item"})},
		decl:        map[string]SearchResult{},
	}
}

func TestResolveToolRefusesAmbiguousName(t *testing.T) {
	snap := inconsistentSnapshot()
	if _, ok := snap.resolveTool("acme", "get_item"); ok {
		t.Fatal("raw match and identifier match point to different tools, must be refused")
	}
	if raw, ok := snap.resolveTool("acme", "get-item"); !ok || raw != "get-item" {
		t.Fatalf("unrelated name: %q %v", raw, ok)
	}
}

func TestResolveToolConsistentSnapshot(t *testing.T) {
	snap := &snapshot{
		byQualified: map[string]catalog.ToolDef{"acme.get-item": {}, "acme.get_item": {}},
		idents:      map[string]*catalog.IdentMap{"acme": catalog.NewIdentMap([]string{"get-item", "get_item"})},
	}
	for in, want := range map[string]string{"get_item": "get_item", "get_item_2": "get-item", "get-item": "get-item", "unknown": "unknown"} {
		if raw, ok := snap.resolveTool("acme", in); !ok || raw != want {
			t.Errorf("resolveTool(%q) = %q %v, want %q", in, raw, ok, want)
		}
	}
}

func TestBridgeDeniesAmbiguousToolAndAudits(t *testing.T) {
	pool := storetest.New(t)
	rec := audit.New(pool, audit.Options{FlushInterval: 20 * time.Millisecond})
	svc := &Service{audit: rec, log: slog.Default(), catalog: &catalogCache{ttl: time.Hour, now: time.Now, snap: inconsistentSnapshot()}}
	subj := policy.Subject{UserID: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", ClientID: "c"}
	x := &execution{svc: svc, subj: subj}

	_, err := x.bridge(context.Background(), "acme.get_item", nil)
	if err == nil || !strings.Contains(err.Error(), "denied: ambiguous_tool") {
		t.Fatalf("err = %v", err)
	}
	if err := rec.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	var decision, reason string
	if err := pool.QueryRow(context.Background(), `SELECT decision, reason FROM audit_events`).Scan(&decision, &reason); err != nil {
		t.Fatal(err)
	}
	if decision != "deny" || reason != "ambiguous_tool" {
		t.Fatalf("audit = %s %s", decision, reason)
	}
}

// shadowSnapshot has an active "get-item" and a quarantined "get_item": the identifier of the
// active tool is also the raw name of the quarantined one.
func shadowSnapshot() *snapshot {
	return &snapshot{
		loaded:      time.Now(),
		byQualified: map[string]catalog.ToolDef{"acme.get-item": {Server: "acme", Name: "get-item"}},
		servers:     map[string]serverInfo{"acme": {id: "1"}},
		idents:      map[string]*catalog.IdentMap{"acme": catalog.NewIdentMap([]string{"get-item"})},
		inactive:    map[string]struct{}{"acme.get_item": {}},
		decl:        map[string]SearchResult{},
	}
}

func TestIsInactiveSeesQuarantinedRawName(t *testing.T) {
	snap := shadowSnapshot()
	if !snap.isInactive("acme", "get_item") {
		t.Fatal("quarantined raw name must be known")
	}
	if snap.isInactive("acme", "get-item") || snap.isInactive("other", "get_item") {
		t.Fatal("active tool or other server must not be inactive")
	}
}

func TestBridgeDeniesQuarantinedNameInsteadOfShadowing(t *testing.T) {
	pool := storetest.New(t)
	rec := audit.New(pool, audit.Options{FlushInterval: 20 * time.Millisecond})
	svc := &Service{audit: rec, log: slog.Default(), catalog: &catalogCache{ttl: time.Hour, now: time.Now, snap: shadowSnapshot()}}
	x := &execution{svc: svc, subj: policy.Subject{UserID: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", ClientID: "c"}}

	// Without the fix this resolved to the active get-item and went on to policy and the upstream.
	_, err := x.bridge(context.Background(), "acme.get_item", nil)
	if err == nil || !strings.Contains(err.Error(), "denied: tool_quarantined") {
		t.Fatalf("err = %v", err)
	}
	if err := rec.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	var tool, decision, reason string
	if err := pool.QueryRow(context.Background(), `SELECT tool_name, decision, reason FROM audit_events`).Scan(&tool, &decision, &reason); err != nil {
		t.Fatal(err)
	}
	if tool != "get_item" || decision != "deny" || reason != "tool_quarantined" {
		t.Fatalf("audit = %s %s %s", tool, decision, reason)
	}
}
