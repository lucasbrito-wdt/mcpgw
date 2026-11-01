package upstream_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

func TestListToolsExposesUpstreamAuthStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := upstream.New()
	defer c.Close()
	_, _, err := c.ListTools(context.Background(), srv.URL+"/mcp", upstream.StaticHeader{Name: "Authorization", Value: "Bearer bad-token"})
	var se *upstream.HTTPStatusError
	if !errors.As(err, &se) || se.Status != http.StatusUnauthorized {
		t.Fatalf("err = %v, want HTTPStatusError 401", err)
	}
	if strings.Contains(err.Error(), "bad-token") {
		t.Fatal("token leaked in error")
	}
}
