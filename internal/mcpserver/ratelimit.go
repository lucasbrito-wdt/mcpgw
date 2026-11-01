package mcpserver

import (
	"math"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const limiterSweepEvery = time.Minute

// RateLimit is a token bucket applied per user.
type RateLimit struct {
	// RPS is the sustained requests per second (default 10).
	RPS float64
	// Burst is the bucket size (default 20).
	Burst int
}

func (r RateLimit) withDefaults() RateLimit {
	if r.RPS <= 0 {
		r.RPS = 10
	}
	if r.Burst <= 0 {
		r.Burst = 20
	}
	return r
}

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// userLimiter is an in-memory per-key token bucket table. Idle buckets are full again, so
// forgetting them changes nothing; they are swept lazily.
type userLimiter struct {
	cfg  RateLimit
	idle time.Duration
	now  func() time.Time

	mu        sync.Mutex
	entries   map[string]*limiterEntry
	lastSweep time.Time
}

func newUserLimiter(cfg RateLimit, now func() time.Time) *userLimiter {
	cfg = cfg.withDefaults()
	idle := time.Duration(float64(cfg.Burst)/cfg.RPS*float64(time.Second)) + time.Minute
	return &userLimiter{cfg: cfg, idle: idle, now: now, entries: map[string]*limiterEntry{}}
}

// allow consumes one token for key. When denied, retryAfter is the whole seconds (at least 1)
// until a token is available.
func (l *userLimiter) allow(key string) (ok bool, retryAfter int) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastSweep) > limiterSweepEvery {
		for k, e := range l.entries {
			if now.Sub(e.lastSeen) > l.idle {
				delete(l.entries, k)
			}
		}
		l.lastSweep = now
	}
	e, found := l.entries[key]
	if !found {
		e = &limiterEntry{limiter: rate.NewLimiter(rate.Limit(l.cfg.RPS), l.cfg.Burst)}
		l.entries[key] = e
	}
	e.lastSeen = now
	res := e.limiter.ReserveN(now, 1)
	if !res.OK() {
		return false, 1
	}
	if d := res.DelayFrom(now); d > 0 {
		res.CancelAt(now)
		return false, max(1, int(math.Ceil(d.Seconds())))
	}
	return true, 0
}
