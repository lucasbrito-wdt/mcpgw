package upstream_test

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

const (
	canaryPassword = "CANARYPW9f3a"
	canaryValue    = "CANARYVAL9f3a"
)

// FuzzRedactURLs wraps a request URL carrying a userinfo password and a query value in the error
// shapes net/http produces. Neither canary may survive, and neither may the path or the query keys:
// only scheme://host[:port] and a REDACTED query marker are kept.
func FuzzRedactURLs(f *testing.F) {
	f.Add("mcp", "api_key", "x=1")
	f.Add("a/b c/%zz", "k", "")
	f.Add("p?a=b", "tok;en", "z=1&z=2")
	f.Add("\"quoted\"\\", "k\x00", "#frag")
	f.Add("", "", "")
	f.Add("[::1", "k", "&&=&")
	f.Fuzz(func(t *testing.T, path, key, extra string) {
		for _, in := range []string{path, key, extra} {
			if strings.Contains(in, "CANARY") {
				return
			}
		}
		pathCanary := "PATHCANARY"
		raw := "https://user:" + canaryPassword + "@host.example/" + pathCanary + path + "?" + pathCanary + key + "=" + canaryValue + "&" + extra
		for _, err := range []error{
			&url.Error{Op: "Post", URL: raw, Err: errors.New("boom")},
			fmt.Errorf("wrapped: %w", &url.Error{Op: "Get", URL: raw, Err: errors.New("dial")}),
			&url.Error{Op: "Get", URL: raw, Err: &url.Error{Op: "Get", URL: raw + "&k=2", Err: errors.New("x")}},
		} {
			msg := upstream.RedactURLs(err)
			if strings.Contains(msg, canaryPassword) || strings.Contains(msg, canaryValue) || strings.Contains(msg, pathCanary) {
				t.Fatalf("secret survived in %q (url %q)", msg, raw)
			}
		}
	})
}
