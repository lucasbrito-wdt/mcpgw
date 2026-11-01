package portalapi_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/admin"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstreamauth"
)

func TestAdminRevokeConnection(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	a := h.loginAs(h.emailA)
	h.headerServer("gh", "good-")
	h.ceiling(h.userA, "gh", "read")
	h.put(a, "/portal/api/connections/gh/token", map[string]string{"token": goodToken})
	svc := admin.New(admin.Options{Pool: h.pool, Credentials: func() (*upstreamauth.Manager, error) { return h.mgr, nil }})
	if err := svc.RevokeConnection(ctx, "gh", h.userA, "Token vazado"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.mgr.CredentialFor(ctx, h.serverID("gh"), h.userA); !errors.Is(err, upstreamauth.ErrNotConnected) {
		t.Fatal(err)
	}
	body := readAll(h.get(a, "/portal/api/connections/gh"))
	if !strings.Contains(body, `"status":"revoked_by_admin"`) || !strings.Contains(body, `"revoked_reason":"Token vazado"`) {
		t.Fatal(body)
	}
	if list := readAll(h.get(a, "/portal/api/connections")); !strings.Contains(list, `"revoked_by_admin":1`) {
		t.Fatal(list)
	}
}
