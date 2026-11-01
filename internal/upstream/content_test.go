package upstream_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

func TestResultCapOverTheWire(t *testing.T) {
	const limit = 20_000
	f := newHTTPFake(t, fakeModern)
	c := upstream.New(upstream.WithMaxResponseBytes(limit))
	defer c.Close()
	ctx := context.Background()

	cases := []struct {
		name      string
		args      string
		wantKept  int
		wantError error
	}{
		{"one image over the cap becomes a placeholder", `{"bytes":15000}`, 0, nil},
		{"second image dropped", `{"bytes":8000,"count":2}`, 1, nil},
		{"wire message over twice the cap fails", `{"bytes":40000}`, 0, upstream.ErrResponseTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := c.CallTool(ctx, f.srv.URL, nil, "images", json.RawMessage(tc.args))
			if tc.wantError != nil {
				if !errors.Is(err, tc.wantError) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Value) > limit {
				t.Fatalf("value of %d bytes exceeds the cap", len(res.Value))
			}
			var items []map[string]any
			if err := json.Unmarshal(res.Value, &items); err != nil {
				t.Fatal(err)
			}
			kept := 0
			for _, it := range items[1:] {
				if it["data"] != nil {
					kept++
				} else if it["omitted"] == nil {
					t.Fatalf("image without data or placeholder: %v", it)
				}
			}
			if items[0]["text"] != "pictures" || kept != tc.wantKept {
				t.Fatalf("kept %d images (value of %d bytes)", kept, len(res.Value))
			}
		})
	}
}

func TestStdioOversizedFrameDiscardsSession(t *testing.T) {
	c, key := stdioClient(t, upstream.WithMaxResponseBytes(10_000))
	ctx := context.Background()
	first := pidOf(t, c, ctx, key)
	_, err := c.CallTool(ctx, key, nil, "images", json.RawMessage(`{"bytes":30000}`))
	if !errors.Is(err, upstream.ErrResponseTooLarge) {
		t.Fatalf("err = %v", err)
	}
	waitGone(t, first)
	if pidOf(t, c, ctx, key) == first {
		t.Fatal("session with a broken stream reused")
	}
}
