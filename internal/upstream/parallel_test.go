package upstream_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

// TestParallelCallsOverlapOnOneSession: a script's Promise.all reaches the upstream as concurrent
// CallTool calls from one owner, which share one pooled session. They must run side by side on
// every transport; a lock held across the call would silently turn Promise.all into a queue.
func TestParallelCallsOverlapOnOneSession(t *testing.T) {
	const (
		calls = 5
		delay = 200 * time.Millisecond
		limit = 2 * delay // serial would take calls*delay = 1s
	)
	endpoints := map[string]func(t *testing.T, res *testResolver) string{
		"http modern": func(t *testing.T, res *testResolver) string {
			f := newHTTPFake(t, fakeModern)
			return res.add(f.srv.URL, upstream.Endpoint{Transport: upstream.TransportAuto, URL: f.srv.URL})
		},
		"http legacy stateful": func(t *testing.T, res *testResolver) string {
			f := newHTTPFake(t, fakeLegacy)
			return res.add(f.srv.URL, upstream.Endpoint{Transport: upstream.TransportAuto, URL: f.srv.URL})
		},
		"legacy sse": func(t *testing.T, res *testResolver) string {
			f := newHTTPFake(t, fakeSSE)
			return res.add(f.srv.URL, upstream.Endpoint{Transport: upstream.TransportAuto, URL: f.srv.URL})
		},
		"stdio modern": func(t *testing.T, res *testResolver) string {
			spec := stdioSpec(t, "modern", nil)
			return res.add("stdio://parallel-modern", upstream.Endpoint{Transport: upstream.TransportStdio, Stdio: &spec})
		},
		"stdio legacy": func(t *testing.T, res *testResolver) string {
			spec := stdioSpec(t, "legacy", nil)
			return res.add("stdio://parallel-legacy", upstream.Endpoint{Transport: upstream.TransportStdio, Stdio: &spec})
		},
	}
	for name, endpoint := range endpoints {
		t.Run(name, func(t *testing.T) {
			res := newTestResolver()
			key := endpoint(t, res)
			c := upstream.New(upstream.WithResolver(res), upstream.WithTerminateGrace(200*time.Millisecond))
			defer c.Close()
			ctx := upstream.WithOwner(context.Background(), "alice")
			callText(t, c, ctx, key, "echo", `{"text":"warm"}`) // connect and detect outside the timing

			start := time.Now()
			var wg sync.WaitGroup
			errs := make(chan error, calls)
			for range calls {
				wg.Go(func() {
					_, err := c.CallTool(ctx, key, nil, "slow", []byte(`{"bytes":200}`))
					errs <- err
				})
			}
			wg.Wait()
			elapsed := time.Since(start)
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			if elapsed > limit {
				t.Fatalf("%d parallel calls of %v took %v: calls were serialized", calls, delay, elapsed)
			}
			t.Logf("%d parallel calls of %v took %v", calls, delay, elapsed)
		})
	}
}
