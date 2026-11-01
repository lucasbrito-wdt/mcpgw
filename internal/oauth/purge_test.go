package oauth

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPurgeExpired(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	now := e.clock.Now()
	insertCode := func(hash string, expires time.Time) {
		_, err := e.pool.Exec(ctx, `INSERT INTO oauth_clients (client_id, registration_kind) VALUES ($1, 'dcr') ON CONFLICT DO NOTHING`, "c1")
		e.must(err)
		_, err = e.pool.Exec(ctx, `INSERT INTO oauth_authorization_codes
			(code_hash, client_id, user_id, redirect_uri, code_challenge, resource, expires_at)
			VALUES ($1, 'c1', $2::uuid, 'http://127.0.0.1/cb', 'x', 'r', $3)`, hash, e.userID, expires)
		e.must(err)
	}
	insertRefresh := func(hash string, expires time.Time) {
		_, err := e.pool.Exec(ctx, `INSERT INTO oauth_refresh_tokens (token_hash, family_id, client_id, user_id, expires_at)
			VALUES ($1, uuidv7(), 'c1', $2::uuid, $3)`, hash, e.userID, expires)
		e.must(err)
	}
	insertCode("old", now.Add(-time.Hour))
	insertCode("fresh", now.Add(time.Hour))
	insertRefresh("old", now.Add(-time.Hour))
	insertRefresh("fresh", now.Add(time.Hour))

	n, err := e.srv.PurgeExpired(ctx)
	if err != nil || n != 2 {
		t.Fatalf("purge = %d, %v", n, err)
	}
	var codes, refresh int
	e.must(e.pool.QueryRow(ctx, `SELECT count(*) FROM oauth_authorization_codes`).Scan(&codes))
	e.must(e.pool.QueryRow(ctx, `SELECT count(*) FROM oauth_refresh_tokens`).Scan(&refresh))
	if codes != 1 || refresh != 1 {
		t.Fatalf("remaining codes=%d refresh=%d, want 1 and 1", codes, refresh)
	}
}

func TestSessionUser(t *testing.T) {
	e := newTestEnv(t)
	if id, ok := e.srv.SessionUser(newGet("/connect/x")); ok || id != "" {
		t.Fatalf("SessionUser without cookie = %q, %v", id, ok)
	}
	rec := httptest.NewRecorder()
	e.must(e.srv.startSession(rec, e.userID))
	r := newGet("/connect/x")
	for _, c := range rec.Result().Cookies() {
		r.AddCookie(c)
	}
	if id, ok := e.srv.SessionUser(r); !ok || id != e.userID {
		t.Fatalf("SessionUser = %q, %v", id, ok)
	}
}
