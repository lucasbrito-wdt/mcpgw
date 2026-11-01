package upstreamauth

import (
	"net/http"
	"testing"
)

func TestConnectCallbackLandsOnPortal(t *testing.T) {
	e := newEnv(t)
	rec := e.callback(e.userA, e.fake.authorize(e.startFlow(e.userA, "alpha")))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/portal/?connected=alpha" {
		t.Fatalf("Location = %q", got)
	}
}
