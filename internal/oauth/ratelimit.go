package oauth

import (
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const limiterMaxKeys = 10000

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// keyedLimiter is an in-memory token bucket per key with bounded memory.
type keyedLimiter struct {
	limit   rate.Limit
	burst   int
	idleTTL time.Duration

	mu      sync.Mutex
	entries map[string]*limiterEntry
}

func newKeyedLimiter(limit rate.Limit, burst int) *keyedLimiter {
	// A bucket idle for this long is full again, so forgetting it changes nothing.
	idle := time.Duration(float64(burst)/float64(limit)*float64(time.Second)) + time.Minute
	return &keyedLimiter{limit: limit, burst: burst, idleTTL: idle, entries: map[string]*limiterEntry{}}
}

// allow consumes one token for key. When the table is full, idle buckets are swept and, if all
// are active, the least recently seen one is evicted: refusing new keys would let an attacker
// that fills the table deny every legitimate client.
func (l *keyedLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.entry(key, now).limiter.AllowN(now, 1)
}

// blocked reports whether key has no token left, without consuming one.
func (l *keyedLimiter) blocked(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[key]
	return ok && e.limiter.TokensAt(now) < 1
}

// spend consumes one token for key unconditionally (used to count failures).
func (l *keyedLimiter) spend(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entry(key, now).limiter.AllowN(now, 1)
}

// reset forgets key, giving it a full bucket again.
func (l *keyedLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}

// entry returns the bucket of key, creating it (and making room) when needed. l.mu must be held.
func (l *keyedLimiter) entry(key string, now time.Time) *limiterEntry {
	e, ok := l.entries[key]
	if !ok {
		if len(l.entries) >= limiterMaxKeys {
			l.sweep(now)
		}
		if len(l.entries) >= limiterMaxKeys {
			l.evictOldest()
		}
		e = &limiterEntry{limiter: rate.NewLimiter(l.limit, l.burst)}
		l.entries[key] = e
	}
	e.lastSeen = now
	return e
}

func (l *keyedLimiter) evictOldest() {
	var oldest string
	var at time.Time
	for k, e := range l.entries {
		if oldest == "" || e.lastSeen.Before(at) {
			oldest, at = k, e.lastSeen
		}
	}
	delete(l.entries, oldest)
}

func (l *keyedLimiter) sweep(now time.Time) {
	for k, e := range l.entries {
		if now.Sub(e.lastSeen) > l.idleTTL {
			delete(l.entries, k)
		}
	}
}

// clientIP is the rate limiting key of the TCP peer: IPv4 by /32 and IPv6 by /64 (the smallest
// allocation to a single subscriber, so one host cannot mint billions of keys). X-Forwarded-For
// is deliberately not trusted: phase 1 listens directly, and behind a reverse proxy this would
// key every client by the proxy address until a trusted-proxy setting is added.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	addr = addr.Unmap().WithZone("")
	if addr.Is6() {
		if p, err := addr.Prefix(64); err == nil {
			return p.String()
		}
	}
	return addr.String()
}
