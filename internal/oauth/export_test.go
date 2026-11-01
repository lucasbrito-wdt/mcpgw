package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// startSession keeps the pre-export name for older tests in this package.
func (s *Server) startSession(w http.ResponseWriter, userID string) error {
	return s.StartSession(w, userID)
}

func TestLoginReturnsUserAndRateLimits(t *testing.T) {
	e := newTestEnv(t)
	r := httptest.NewRequest(http.MethodPost, "/admin/api/session", nil)
	r.RemoteAddr = "203.0.113.7:1234"
	id, err := e.srv.Login(r.Context(), r, testEmail, testPassword)
	if err != nil || id != e.userID {
		t.Fatalf("Login = %q, %v", id, err)
	}
	if _, err := e.srv.Login(r.Context(), r, testEmail, "wrong"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("bad password: %v", err)
	}
	for i := 0; i < 10; i++ {
		_, err = e.srv.Login(r.Context(), r, testEmail, "wrong")
	}
	if !errors.Is(err, ErrLoginRateLimited) {
		t.Fatalf("no rate limit: %v", err)
	}
}

func TestRevokeFamilyRejectsCachedAccessToken(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	p := newPKCE()
	tr := decodeTokens(t, e.exchange(e.authorizeCode(p), p.verifier, callback))
	claims, err := e.srv.ValidateAccessToken(ctx, tr.AccessToken) // now in the grant cache
	if err != nil {
		t.Fatal(err)
	}
	if err := e.srv.RevokeFamily(ctx, claims.FamilyID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.srv.ValidateAccessToken(ctx, tr.AccessToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("still valid: %v", err)
	}
}

func TestSessionSlidesWithAbsoluteCap(t *testing.T) {
	e := newTestEnv(t)
	rec := httptest.NewRecorder()
	if err := e.srv.StartSession(rec, e.userID); err != nil {
		t.Fatal(err)
	}
	cookie := rec.Result().Cookies()[0]
	touch := func() bool {
		r := httptest.NewRequest(http.MethodGet, "/portal/api/me", nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		uid, ok := e.srv.TouchSession(w, r)
		if cs := w.Result().Cookies(); len(cs) == 1 {
			cookie = cs[0]
		}
		return ok && uid == e.userID
	}
	for i := 0; i < 24; i++ { // 24 x 29 min = 11h36, every gap below the 30 min idle limit
		e.clock.Advance(29 * time.Minute)
		if !touch() {
			t.Fatalf("touch %d failed", i)
		}
	}
	e.clock.Advance(25 * time.Minute) // 12h01 since login, idle only 25 min
	if touch() {
		t.Fatal("session outlived the 12 h cap")
	}
}

func TestSessionIdleTimeout(t *testing.T) {
	e := newTestEnv(t)
	rec := httptest.NewRecorder()
	if err := e.srv.StartSession(rec, e.userID); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/connect/x", nil)
	r.AddCookie(rec.Result().Cookies()[0])
	if id, ok := e.srv.SessionUser(r); !ok || id != e.userID {
		t.Fatalf("fresh session = %q, %v", id, ok)
	}
	e.clock.Advance(31 * time.Minute)
	if id, ok := e.srv.SessionUser(r); ok || id != "" {
		t.Fatalf("idle session = %q, %v", id, ok)
	}
}

func TestTouchSessionRejectsForgedCookie(t *testing.T) {
	e := newTestEnv(t)
	r := httptest.NewRequest(http.MethodGet, "/portal/api/me", nil)
	r.AddCookie(&http.Cookie{Name: e.srv.cookieName(sessionCookie), Value: "garbage"})
	w := httptest.NewRecorder()
	if uid, ok := e.srv.TouchSession(w, r); ok || uid != "" || len(w.Result().Cookies()) != 0 {
		t.Fatalf("forged cookie accepted: %q %v", uid, ok)
	}
}
