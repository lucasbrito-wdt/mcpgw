package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lucasbrito-wdt/mcpgw/internal/catalog"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

// FuzzBridgeNameResolution covers how the bridge turns the name a script passed to tools.call
// into a raw upstream tool: ParseQualified, the inactive-tool guard and resolveTool.
func FuzzBridgeNameResolution(f *testing.F) {
	f.Add("acme.get-item", "get-item\x1fget_item\x1fclass", "acme.old")
	f.Add("acme.get_item_2", "get-item\x1fget_item", "")
	f.Add("acme.class_", "class", "acme.class_")
	f.Add("acme.", "x", "")
	f.Add(".x", "x", "")
	f.Add("acme.a.b", "a.b\x1fa_b", "")
	f.Fuzz(func(t *testing.T, qualified, joinedTools, inactive string) {
		const server = "acme"
		names := strings.Split(joinedTools, "\x1f")
		snap := &snapshot{
			byQualified: map[string]catalog.ToolDef{},
			idents:      map[string]*catalog.IdentMap{server: catalog.NewIdentMap(names)},
			inactive:    map[string]struct{}{},
		}
		for _, n := range names {
			snap.byQualified[catalog.QualifiedName(server, n)] = catalog.ToolDef{Server: server, Name: n}
		}
		if inactive != "" {
			snap.inactive[inactive] = struct{}{}
		}

		srv, tool, ok := catalog.ParseQualified(qualified)
		if !ok {
			return
		}
		raw, resolved := snap.resolveTool(srv, tool)
		if !resolved {
			return
		}
		if srv != server {
			if raw != tool {
				t.Fatalf("unknown server %q rewrote tool %q to %q", srv, tool, raw)
			}
			return
		}
		// A name that resolves must be a known raw tool or pass through unchanged for policy to deny.
		if _, known := snap.byQualified[catalog.QualifiedName(server, raw)]; !known && raw != tool {
			t.Fatalf("%q resolved to unknown raw name %q", tool, raw)
		}
		if snap.isInactive(srv, tool) {
			// The bridge denies before resolving; resolution itself must not turn an inactive
			// exact name into a different active tool unless the identifier map says so.
			if _, exact := snap.byQualified[qualified]; exact && raw != tool {
				t.Fatalf("exact raw tool %q resolved to %q", tool, raw)
			}
		}
	})
}

type fuzzCredential struct{ value string }

func (c fuzzCredential) Apply(r *http.Request) { r.Header.Set("Authorization", "Bearer "+c.value) }

// FuzzUpstreamErrorScrub checks that an upstream message echoing the credential is scrubbed and
// bounded before it reaches the model.
func FuzzUpstreamErrorScrub(f *testing.F) {
	f.Add("sk-live-0123456789abcdef", "bad token sk-live-0123456789abcdef rejected")
	f.Add("abcdefgh", "abcdefghabcdefgh")
	f.Add("[redacted]xxxx", "x[redacted]xxxx")
	f.Add("short", "short")
	f.Add("\xff\xfe\xfd\xfc\xfb\xfa\xf9\xf8", "é\xff\xfe\xfd\xfc\xfb\xfa\xf9\xf8")
	f.Fuzz(func(t *testing.T, token, message string) {
		if strings.TrimSpace(token) != token || strings.ContainsAny(token, " \t\r\n") {
			return // header values are trimmed on the wire: whitespace-padded tokens are not a real credential
		}
		secrets := credentialSecrets(fuzzCredential{token})
		for _, err := range []error{
			&upstream.ToolError{Message: message},
			errors.New(message),
		} {
			out := upstreamError(err, secrets).Error()
			// Scrubbing inserts the placeholder, which can spell part of another secret.
			visible := strings.ReplaceAll(out, redactedToken, "")
			for _, s := range secrets {
				if strings.Contains(visible, string(s)) && !strings.Contains(strings.ReplaceAll(message, string(s), ""), string(s)) {
					t.Fatalf("secret %q survived: %q", s, out)
				}
			}
			if n := utf8.RuneCountInString(out); n > maxUpstreamErrorChars+64 {
				t.Fatalf("error not bounded: %d runes", n)
			}
		}
	})
}

// FuzzCallToolArgs feeds arbitrary bytes through the argument decoding of upstream.Client.CallTool
// (what a script's tools.call args become). It must never panic; the dial always fails fast.
func FuzzCallToolArgs(f *testing.F) {
	for _, s := range []string{``, `null`, `{}`, `[]`, `"x"`, `{"a":1}`, `{"a":`, `{"a":1e999}`, `{"\ud800":"\u0000"}`, `{"a":{"b":[1,2,{"c":null}]}}`, strings.Repeat(`[`, 5000)} {
		f.Add(s)
	}
	client := upstream.New(
		upstream.WithTransport(&http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("dial disabled in fuzz")
		}}),
	)
	f.Fuzz(func(t *testing.T, args string) {
		_, err := client.CallTool(context.Background(), "http://127.0.0.1:1/mcp", nil, "tool", json.RawMessage(args))
		if err == nil {
			t.Fatal("call without a server cannot succeed")
		}
	})
}
