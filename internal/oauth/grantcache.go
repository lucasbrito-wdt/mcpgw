package oauth

import (
	"sync"
	"time"
)

// grantCacheTTL bounds how long a revocation made outside this process (admin CLI, SQL) can go
// unnoticed by access-token validation. Revocations made here (revokeFamily) take effect at once.
const grantCacheTTL = 5 * time.Second

// grantCacheMax caps the entries; past it the cache is cleared (it only saves queries).
const grantCacheMax = 50_000

type grantKey struct{ clientID, userID, familyID string }

// grantCache remembers grants that checkGrantActive found active. Negative results are never
// cached, so a broken or revoked grant is re-checked on every request.
type grantCache struct {
	mu      sync.Mutex
	active  map[grantKey]time.Time // expiry
	revoked map[string]bool        // families revoked in this process, checked before active
}

func newGrantCache() *grantCache {
	return &grantCache{active: map[grantKey]time.Time{}, revoked: map[string]bool{}}
}

func (g *grantCache) ok(k grantKey, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	exp, found := g.active[k]
	return found && now.Before(exp) && !g.revoked[k.familyID]
}

func (g *grantCache) put(k grantKey, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.revoked[k.familyID] {
		return // a query that started before the revocation must not re-cache the grant
	}
	if len(g.active) >= grantCacheMax {
		clear(g.active)
	}
	g.active[k] = now.Add(grantCacheTTL)
}

// revoke drops every cached grant of the family and keeps it out of the cache for good.
func (g *grantCache) revoke(familyID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for k := range g.active {
		if k.familyID == familyID {
			delete(g.active, k)
		}
	}
	if len(g.revoked) >= grantCacheMax {
		clear(g.revoked) // the database still rejects these families; only the shortcut is lost
	}
	g.revoked[familyID] = true
}
