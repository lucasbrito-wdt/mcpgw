package upstreamauth

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/vault"
)

func (e *env) setProfileToken(serverID, userID, profile, token string) {
	e.t.Helper()
	if _, err := e.mgr.SetProfileToken(ctx, e.pool, serverID, userID, profile, token); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) profileHeader(serverID, userID, profile string) string {
	e.t.Helper()
	cred, err := e.mgr.ProfileCredentialFor(ctx, serverID, userID, profile)
	return headerValue(e.t, cred, err)
}

func TestDefaultProfileKeepsLegacyAAD(t *testing.T) {
	e := newEnv(t)
	id := e.insertHeaderServer("gh", "https://gh.example/mcp", "Authorization", "Bearer ", "")
	// Sealed exactly as before migration 0008: AAD without a profile, row inserted without one.
	ct, err := e.vault.Seal([]byte("legacy-token-1"), []byte("upstream_credentials:"+id+":"+e.userA))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO upstream_credentials (server_id, user_id, access_ciphertext, key_id)
		VALUES ($1::uuid, $2::uuid, $3, 'k1')`, id, e.userA, ct); err != nil {
		t.Fatal(err)
	}
	cred, err := e.mgr.CredentialFor(ctx, id, e.userA)
	if got := headerValue(t, cred, err); got != "Bearer legacy-token-1" {
		t.Fatalf("CredentialFor got %q", got)
	}
	for _, p := range []string{"", DefaultProfile} {
		if got := e.profileHeader(id, e.userA, p); got != "Bearer legacy-token-1" {
			t.Fatalf("profile %q got %q", p, got)
		}
	}
	// A named profile binds its name into the AAD: the legacy ciphertext must not open under it.
	if _, err := e.pool.Exec(ctx, `UPDATE upstream_credentials SET profile = 'work'`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.mgr.ProfileCredentialFor(ctx, id, e.userA, "work"); !errors.Is(err, vault.ErrDecrypt) {
		t.Fatalf("relabelled row must fail to open, got %v", err)
	}
}

func TestCallUsesNamedProfile(t *testing.T) {
	e := newEnv(t)
	id := e.insertHeaderServer("gh", "https://gh.example/mcp", "Authorization", "Bearer ", "")
	e.setSharedToken(id, "shared-token-0")
	e.setProfileToken(id, e.userA, DefaultProfile, "default-token-1")
	e.setProfileToken(id, e.userA, "work", "work-token-2")
	e.setProfileToken(id, e.userA, "side_b-2", "side-token-3")

	for profile, want := range map[string]string{"": "default-token-1", "default": "default-token-1",
		"work": "work-token-2", "side_b-2": "side-token-3"} {
		if got := e.profileHeader(id, e.userA, profile); got != "Bearer "+want {
			t.Fatalf("profile %q got %q", profile, got)
		}
	}

	_, err := e.mgr.ProfileCredentialFor(ctx, id, e.userA, "missing")
	var nc *NotConnectedError
	if !errors.Is(err, ErrNotConnected) || !errors.As(err, &nc) || nc.Profile != "missing" ||
		nc.ConnectURL != "http://localhost:8787/portal/connect/?slug=gh&profile=missing" ||
		!strings.Contains(err.Error(), `"missing"`) {
		t.Fatalf("missing profile: %v", err)
	}
	// userB has no personal "work" row: the shared row must never stand in for a named profile.
	if _, err := e.mgr.ProfileCredentialFor(ctx, id, e.userB, "work"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("named profile fell back to shared: %v", err)
	}
	if _, err := e.mgr.ProfileCredentialFor(ctx, id, "", "work"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("catalog sync with named profile: %v", err)
	}
}

func TestCallWithoutProfileUsesDefault(t *testing.T) {
	e := newEnv(t)
	id := e.insertHeaderServer("gh", "https://gh.example/mcp", "Authorization", "Bearer ", "")
	e.setSharedToken(id, "shared-token-0")
	e.setProfileToken(id, e.userB, "work", "work-token-2")
	if got := e.profileHeader(id, e.userB, ""); got != "Bearer shared-token-0" {
		t.Fatalf("only a named profile: got %q, want shared", got)
	}
	e.setProfileToken(id, e.userB, DefaultProfile, "default-token-1")
	if got := e.profileHeader(id, e.userB, ""); got != "Bearer default-token-1" {
		t.Fatalf("personal default: got %q", got)
	}
	cred, err := e.mgr.CredentialFor(ctx, id, e.userB)
	if got := headerValue(t, cred, err); got != "Bearer default-token-1" {
		t.Fatalf("CredentialFor got %q", got)
	}
}

func TestAnyCredentialForFallsBackToFirstNamedProfile(t *testing.T) {
	e := newEnv(t)
	id := e.insertHeaderServer("gh", "https://gh.example/mcp", "Authorization", "Bearer ", "")
	any := func(userID string) (string, error) {
		cred, err := e.mgr.AnyCredentialFor(ctx, id, userID)
		if err != nil {
			return "", err
		}
		return headerValue(t, cred, nil), nil
	}
	if _, err := any(e.userA); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("no profiles: %v", err)
	}
	e.setProfileToken(id, e.userA, "globex", "globex-token")
	e.setProfileToken(id, e.userA, "acme", "acme-token")
	if got, err := any(e.userA); err != nil || got != "Bearer acme-token" {
		t.Fatalf("named only: got %q, %v", got, err)
	}
	if _, err := any(""); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("empty user must not fall back: %v", err)
	}
	e.setProfileToken(id, e.userA, DefaultProfile, "default-token")
	if got, err := any(e.userA); err != nil || got != "Bearer default-token" {
		t.Fatalf("default wins: got %q, %v", got, err)
	}
}

func TestConnectStoresProfileFromState(t *testing.T) {
	e := newEnv(t)
	authURL := e.startFlow(e.userA, "alpha?profile=work")
	var stateProfile string
	if err := e.pool.QueryRow(ctx, `SELECT profile FROM upstream_oauth_states`).Scan(&stateProfile); err != nil || stateProfile != "work" {
		t.Fatalf("state profile = %q, %v", stateProfile, err)
	}
	if rec := e.callback(e.userA, e.fake.authorize(authURL)); rec.Code != http.StatusSeeOther {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
	}
	var profile string
	if err := e.pool.QueryRow(ctx, `SELECT profile FROM upstream_credentials`).Scan(&profile); err != nil || profile != "work" {
		t.Fatalf("row profile = %q, %v", profile, err)
	}
	if _, err := e.mgr.ProfileCredentialFor(ctx, e.serverID, e.userA, "work"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.mgr.CredentialFor(ctx, e.serverID, e.userA); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("default must stay unconnected: %v", err)
	}

	e.connect(e.userA, "alpha")
	e.connect(e.userA, "alpha?profile=work") // reconnecting a name replaces it
	if n := e.credentialCount(); n != 2 {
		t.Fatalf("rows = %d, want 2", n)
	}
	work := bearerValue(t, must(e.mgr.ProfileCredentialFor(ctx, e.serverID, e.userA, "work")))
	def := bearerValue(t, must(e.mgr.CredentialFor(ctx, e.serverID, e.userA)))
	if work == def {
		t.Fatalf("profiles share token %q", work)
	}
	if got := e.mgr.ConnectURLForProfile("alpha", "work"); got != "http://localhost:8787/connect/alpha?profile=work" {
		t.Fatalf("ConnectURLForProfile = %q", got)
	}
	if got := e.mgr.ConnectURLForProfile("alpha", ""); got != e.mgr.ConnectURL("alpha") {
		t.Fatalf("default ConnectURLForProfile = %q", got)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestConnectRejectsBadProfile(t *testing.T) {
	e := newEnv(t)
	for _, target := range []string{"/connect/alpha?profile=Work", "/connect/alpha?profile=a%20b",
		"/connect/alpha?shared=1&profile=work"} {
		if rec := e.get(e.userA, target); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d", target, rec.Code)
		}
	}
	if e.credentialCount() != 0 {
		t.Fatal("stored a credential")
	}
}

func TestProfilesListsConnected(t *testing.T) {
	e := newEnv(t)
	id := e.insertHeaderServer("gh", "https://gh.example/mcp", "Authorization", "Bearer ", "")
	e.setSharedToken(id, "shared-token-0")
	e.setProfileToken(id, e.userA, "work", "work-token-2")
	e.setProfileToken(id, e.userA, DefaultProfile, "default-token-1")
	e.setProfileToken(id, e.userA, "broken", "broken-token-3")
	if _, err := e.pool.Exec(ctx, `UPDATE upstream_credentials SET status = 'refresh_failed', access_ciphertext = NULL
		WHERE profile = 'broken'`); err != nil {
		t.Fatal(err)
	}
	check := func(user string, want []string) {
		t.Helper()
		got, err := e.mgr.Profiles(ctx, id, user)
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("Profiles(%s) = %v, %v; want %v", user, got, err, want)
		}
	}
	check(e.userA, []string{"default", "work"})
	check(e.userB, []string{"default"})
	if err := e.mgr.Disconnect(ctx, e.pool, id, "", true); err != nil {
		t.Fatal(err)
	}
	check(e.userB, []string{})
	check(e.userA, []string{"default", "work"})
}

func TestDisconnectProfileDeletesOnlyThatProfile(t *testing.T) {
	e := newEnv(t)
	id := e.insertHeaderServer("gh", "https://gh.example/mcp", "Authorization", "Bearer ", "")
	e.setProfileToken(id, e.userA, DefaultProfile, "default-token-1")
	e.setProfileToken(id, e.userA, "work", "work-token-2")
	if err := e.mgr.Disconnect(ctx, e.pool, id, e.userA, false); err != nil {
		t.Fatal(err)
	}
	if got := e.profileHeader(id, e.userA, "work"); got != "Bearer work-token-2" {
		t.Fatalf("Disconnect removed a named profile: %q", got)
	}
	if err := e.mgr.DisconnectProfile(ctx, e.pool, id, e.userA, "work"); err != nil {
		t.Fatal(err)
	}
	if n := e.credentialCount(); n != 0 {
		t.Fatalf("rows = %d", n)
	}
}

func TestProfileNameValidation(t *testing.T) {
	for name, want := range map[string]bool{"default": true, "work": true, "a": true, "globex_co-2": true,
		"": false, "Work": false, "2work": false, "-x": false, "a b": false, "a.b": false,
		strings.Repeat("a", 32): true, strings.Repeat("a", 33): false} {
		if ValidProfile(name) != want {
			t.Fatalf("ValidProfile(%q) != %v", name, want)
		}
	}
	e := newEnv(t)
	id := e.insertHeaderServer("gh", "https://gh.example/mcp", "Authorization", "Bearer ", "")
	if _, err := e.mgr.SetProfileToken(ctx, e.pool, id, e.userA, "Bad!", "good-token-1234"); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("SetProfileToken: %v", err)
	}
	if _, err := e.mgr.SetProfileToken(ctx, e.pool, id, e.userA, "", "good-token-1234"); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("SetProfileToken empty: %v", err)
	}
	if err := e.mgr.DisconnectProfile(ctx, e.pool, id, e.userA, "Bad!"); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("DisconnectProfile: %v", err)
	}
	if _, err := e.mgr.ProfileCredentialFor(ctx, id, e.userA, "Bad!"); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("ProfileCredentialFor: %v", err)
	}
	if e.credentialCount() != 0 {
		t.Fatal("stored a credential")
	}
}

func TestProfileSchemaConstraints(t *testing.T) {
	e := newEnv(t)
	id := e.insertHeaderServer("gh", "https://gh.example/mcp", "Authorization", "Bearer ", "")
	if _, err := e.pool.Exec(ctx, `INSERT INTO upstream_credentials (server_id, user_id, shared, profile, access_ciphertext, key_id)
		VALUES ($1::uuid, NULL, true, 'work', 'x', 'k1')`, id); err == nil {
		t.Fatal("shared row with a named profile was accepted")
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO upstream_credentials (server_id, user_id, profile, access_ciphertext, key_id)
		VALUES ($1::uuid, $2::uuid, 'Bad', 'x', 'k1')`, id, e.userA); err == nil {
		t.Fatal("invalid profile name was accepted")
	}
	insertAudit := func(profile any) error {
		_, err := e.pool.Exec(ctx, `INSERT INTO audit_events (user_id, client_id, server_slug, tool_name, decision, profile)
			VALUES ($1::uuid, 'c', 'gh', 't', 'allow', $2)`, e.userA, profile)
		return err
	}
	if err := insertAudit("work"); err != nil {
		t.Fatalf("audit with profile: %v", err)
	}
	if err := insertAudit(nil); err != nil {
		t.Fatalf("audit without profile: %v", err)
	}
	if err := insertAudit("Bad!"); err == nil {
		t.Fatal("audit accepted an invalid profile")
	}
}
