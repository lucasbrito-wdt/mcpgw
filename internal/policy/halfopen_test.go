package policy

import (
	"context"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// blackholeProxy forwards TCP to target until blackhole is set, then silently drops all bytes
// in both directions without closing anything: a half-open connection.
type blackholeProxy struct {
	ln        net.Listener
	blackhole atomic.Bool
}

func newBlackholeProxy(t *testing.T, target string) *blackholeProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &blackholeProxy{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("tcp", target)
			if err != nil {
				_ = c.Close()
				continue
			}
			t.Cleanup(func() { _ = c.Close(); _ = up.Close() })
			go p.pipe(up, c)
			go p.pipe(c, up)
		}
	}()
	return p
}

func (p *blackholeProxy) pipe(dst, src net.Conn) {
	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if n > 0 && !p.blackhole.Load() {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func TestHalfOpenListenConnectionFallsBackToDB(t *testing.T) {
	f := newFixture(t, false)
	cfg := f.pool.Config().ConnConfig
	proxy := newBlackholeProxy(t, net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))))

	pcfg := f.pool.Config().Copy()
	pcfg.ConnConfig.Host = "127.0.0.1"
	pcfg.ConnConfig.Port = uint16(proxy.ln.Addr().(*net.TCPAddr).Port)
	pcfg.ConnConfig.Fallbacks = nil
	pcfg.ConnConfig.TLSConfig = nil
	viaProxy, err := pgxpool.NewWithConfig(context.Background(), pcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(viaProxy.Close)

	e, err := New(context.Background(), viaProxy, WithHeartbeat(100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	lctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = e.Listen(lctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, "listener healthy", e.healthy.Load)

	f.setCeiling(t, LevelRead)
	f.setConsent(t, LevelRead)
	decide := func() Decision { return e.Decide(context.Background(), f.subject(), "srv", "tool_read") }
	waitFor(t, "allow", func() bool { return decide().Allow })

	// The notification for the revocation is swallowed by the half-open connection.
	proxy.blackhole.Store(true)
	exec(t, f.pool, `UPDATE consents SET revoked_at = now()`)
	start := time.Now()
	waitFor(t, "heartbeat to mark the engine unhealthy", func() bool { return !e.healthy.Load() })
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("took %v to notice the half-open connection", took)
	}
	proxy.blackhole.Store(false)
	// Unhealthy engines bypass the cache; the revocation must be visible.
	if d := decide(); d.Allow {
		t.Fatalf("stale allow after half-open connection: %+v", d)
	}
}
