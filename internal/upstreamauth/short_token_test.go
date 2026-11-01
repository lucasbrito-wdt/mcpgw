package upstreamauth

import (
	"errors"
	"testing"
)

func TestShortTokenRejected(t *testing.T) {
	e := newEnv(t)
	id := e.insertHeaderServer("gh", "https://gh.example/mcp", "Authorization", "Bearer ", "")
	for _, shared := range []bool{false, true} {
		if _, err := e.mgr.SetToken(ctx, e.pool, id, e.userA, "1234567", shared); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("shared=%v: 7-char token err = %v", shared, err)
		}
	}
	if _, err := e.mgr.TestToken(ctx, nil, id, e.userA, "1234567"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("TestToken err = %v", err)
	}
	if e.credentialCount() != 0 {
		t.Fatal("a short token was stored")
	}
	if _, err := e.mgr.SetToken(ctx, e.pool, id, e.userA, "12345678", false); err != nil {
		t.Fatalf("8-char token: %v", err)
	}
}
