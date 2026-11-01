package upstream_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

func stdioClient(t *testing.T, opts ...upstream.Option) (*upstream.Client, string) {
	t.Helper()
	res := newTestResolver()
	spec := stdioSpec(t, "modern", nil)
	key := res.add("stdio://pool", upstream.Endpoint{Transport: upstream.TransportStdio, Stdio: &spec})
	c := upstream.New(append([]upstream.Option{upstream.WithResolver(res), upstream.WithTerminateGrace(200 * time.Millisecond)}, opts...)...)
	t.Cleanup(c.Close)
	return c, key
}

func pidOf(t *testing.T, c *upstream.Client, ctx context.Context, key string) int {
	t.Helper()
	pid, err := strconv.Atoi(callText(t, c, ctx, key, "pid", "{}"))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("process %d still alive", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestPoolReusesWarmStdioProcessPerOwner(t *testing.T) {
	c, key := stdioClient(t)
	alice := upstream.WithOwner(context.Background(), "alice")
	bob := upstream.WithOwner(context.Background(), "bob")

	a1, a2 := pidOf(t, c, alice, key), pidOf(t, c, alice, key)
	b1 := pidOf(t, c, bob, key)
	if a1 != a2 {
		t.Fatalf("same owner got two processes: %d %d", a1, a2)
	}
	if a1 == b1 {
		t.Fatal("two owners shared one process")
	}
}

func TestPoolIdleTTLEviction(t *testing.T) {
	c, key := stdioClient(t, upstream.WithIdleTTL(150*time.Millisecond))
	ctx := context.Background()
	first := pidOf(t, c, ctx, key)
	waitGone(t, first) // evicted and killed after the idle TTL
	if second := pidOf(t, c, ctx, key); second == first {
		t.Fatal("evicted process reused")
	}
}

func TestPoolReconnectsAfterProcessDeath(t *testing.T) {
	c, key := stdioClient(t)
	ctx := context.Background()
	first := pidOf(t, c, ctx, key)
	if _, err := c.CallTool(ctx, key, nil, "exit", nil); err == nil {
		t.Fatal("exit call should fail: the process died mid-call")
	}
	waitGone(t, first)
	if second := pidOf(t, c, ctx, key); second == first {
		t.Fatal("dead process reused")
	}
}

func TestPoolReconnectOnceOnSessionLoss(t *testing.T) {
	f := newHTTPFake(t, fakeLegacy)
	c := upstream.New()
	defer c.Close()
	ctx := context.Background()
	for range 3 {
		callText(t, c, ctx, f.srv.URL, "echo", `{"text":"a"}`)
	}
	if n := f.inits.Load(); n != 1 {
		t.Fatalf("legacy session not reused: %d initializes", n)
	}
	f.reset(fakeLegacy) // server forgets every session: the next request gets 404
	if got := callText(t, c, ctx, f.srv.URL, "echo", `{"text":"b"}`); got != "b" {
		t.Fatalf("echo after reset = %q", got)
	}
	if n := f.inits.Load(); n != 2 { // one per server instance: the call was retried once
		t.Fatalf("expected one new handshake, got %d total", n)
	}
}

func TestPoolIsolatesCredentials(t *testing.T) {
	f := newHTTPFake(t, fakeLegacy)
	c := upstream.New()
	defer c.Close()
	ctx := context.Background()
	for _, tok := range []string{"token-aaaa", "token-bbbb", "token-aaaa"} {
		if _, err := c.CallTool(ctx, f.srv.URL, upstream.BearerToken(tok), "echo", []byte(`{"text":"x"}`)); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.inits.Load(); n != 2 {
		t.Fatalf("want one session per credential (2), got %d", n)
	}
}

func TestPoolExhausted(t *testing.T) {
	c, key := stdioClient(t, upstream.WithMaxSessions(1))
	alice := upstream.WithOwner(context.Background(), "alice")
	done := make(chan error, 1)
	go func() {
		_, err := c.CallTool(alice, key, nil, "slow", []byte(`{"bytes":700}`))
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	_, err := c.CallTool(upstream.WithOwner(context.Background(), "bob"), key, nil, "pid", nil)
	if !errors.Is(err, upstream.ErrPoolExhausted) {
		t.Fatalf("err = %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// Once alice's session is idle it can be evicted for bob.
	pidOf(t, c, upstream.WithOwner(context.Background(), "bob"), key)
}

func TestPoolConcurrentUse(t *testing.T) {
	f := newHTTPFake(t, fakeLegacy)
	res := newTestResolver()
	spec := stdioSpec(t, "modern", nil)
	stdioKey := res.add("stdio://concurrent", upstream.Endpoint{Transport: upstream.TransportStdio, Stdio: &spec})
	res.add(f.srv.URL, upstream.Endpoint{Transport: upstream.TransportAuto, URL: f.srv.URL})
	c := upstream.New(upstream.WithResolver(res))
	defer c.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for i := range 100 {
		wg.Go(func() {
			ctx := upstream.WithOwner(context.Background(), fmt.Sprint("user", i%4))
			key := stdioKey
			if (i/4)%2 == 0 { // both keys see all four owners
				key = f.srv.URL
			}
			text := fmt.Sprint("m", i)
			res, err := c.CallTool(ctx, key, nil, "echo", []byte(`{"text":"`+text+`"}`))
			if err == nil && string(res.Value) != `"`+text+`"` {
				err = fmt.Errorf("got %s want %s", res.Value, text)
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := f.inits.Load(); n != 4 {
		t.Fatalf("want one HTTP session per owner (4), got %d", n)
	}
}

func TestCloseDrainsSessions(t *testing.T) {
	c, key := stdioClient(t)
	var pids []int
	for _, owner := range []string{"a", "b", "c"} {
		pids = append(pids, pidOf(t, c, upstream.WithOwner(context.Background(), owner), key))
	}
	c.Close()
	for _, pid := range pids {
		if processAlive(pid) {
			t.Fatalf("process %d survived Close", pid)
		}
	}
	if _, err := c.CallTool(context.Background(), key, nil, "pid", nil); !errors.Is(err, upstream.ErrClosed) {
		t.Fatalf("call after Close: %v", err)
	}
}
