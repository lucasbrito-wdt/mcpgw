package main

import (
	"strings"
	"testing"
)

func newSetAdminHarness(t *testing.T) *adminHarness {
	t.Helper()
	return newAdminHarness(t, map[string]string{envAdminPassword: "a long enough password"})
}

func TestUserSetAdminPromotesAndDemotes(t *testing.T) {
	h := newSetAdminHarness(t)
	h.mustRun("user create --email a@example.com")
	h.mustRun("user create --email b@example.com --admin")
	out := h.mustRun("user set-admin --email a@example.com --admin=true")
	if !strings.Contains(out, "a@example.com is now an admin") {
		t.Fatalf("out = %q", out)
	}
	out = h.mustRun("user set-admin --email a@example.com --admin=false")
	if !strings.Contains(out, "a@example.com is no longer an admin") {
		t.Fatalf("out = %q", out)
	}
}

func TestUserSetAdminRefusesLastAdmin(t *testing.T) {
	h := newSetAdminHarness(t)
	h.mustRun("user create --email a@example.com")
	h.mustRun("user create --email b@example.com --admin")
	_, err := h.run("user set-admin --email b@example.com --admin=false")
	if err == nil || !strings.Contains(err.Error(), "cannot remove the last admin") {
		t.Fatalf("err = %v", err)
	}
}

func TestUserSetAdminRequiresAdminFlag(t *testing.T) {
	h := newSetAdminHarness(t)
	h.mustRun("user create --email a@example.com")
	if _, err := h.run("user set-admin --email a@example.com"); err == nil || !strings.Contains(err.Error(), "--admin") {
		t.Fatalf("err = %v", err)
	}
}

func TestUserCreateAdminFlag(t *testing.T) {
	h := newSetAdminHarness(t)
	h.mustRun("user create --email c@example.com --admin")
	h.mustRun("user create --email d@example.com")
	for _, line := range strings.Split(h.mustRun("user list"), "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) > 1 && f[0] == "c@example.com" && f[1] != "yes":
			t.Fatalf("c is not admin: %q", line)
		case len(f) > 1 && f[0] == "d@example.com" && f[1] != "no":
			t.Fatalf("d is admin: %q", line)
		}
	}
}
