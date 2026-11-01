package policy

import (
	"context"
	"slices"
)

// gens is a generation snapshot. A load may be stored in the cache only if the generations it
// observed before querying the DB are unchanged afterwards: an invalidation that lands while the
// query is in flight bumps a generation and the possibly stale result is discarded.
type gens struct{ epoch, scope uint64 }

func (e *Engine) snapshot(userID string, forTools bool) gens {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if forTools {
		return gens{e.epoch, e.toolsGen}
	}
	return gens{e.epoch, e.userGens[userID]}
}

func (e *Engine) getTools(ctx context.Context) (*toolSet, error) {
	if e.healthy.Load() {
		e.mu.RLock()
		t := e.tools
		e.mu.RUnlock()
		if t != nil {
			return t, nil
		}
	}
	for range maxLoadAttempts {
		wasHealthy := e.healthy.Load()
		before := e.snapshot("", true)
		t, err := loadTools(ctx, e.pool)
		if err != nil {
			return nil, err
		}
		if !wasHealthy {
			return t, nil // bypass mode: fresh read, nothing cached
		}
		if e.storeTools(t, before) {
			return t, nil
		}
	}
	return nil, errStaleLoad
}

func (e *Engine) storeTools(t *toolSet, before gens) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if before != (gens{e.epoch, e.toolsGen}) || !e.healthy.Load() {
		return false
	}
	e.tools = t
	return true
}

func (e *Engine) getUser(ctx context.Context, userID string) (*userPolicy, error) {
	if e.healthy.Load() {
		e.mu.RLock()
		u := e.users[userID]
		e.mu.RUnlock()
		if u != nil {
			return u, nil
		}
	}
	for range maxLoadAttempts {
		wasHealthy := e.healthy.Load()
		before := e.snapshot(userID, false)
		u, err := loadUser(ctx, e.pool, userID)
		if err != nil {
			return nil, err
		}
		if !wasHealthy {
			return u, nil
		}
		if e.storeUser(userID, u, before) {
			return u, nil
		}
	}
	return nil, errStaleLoad
}

func (e *Engine) storeUser(userID string, u *userPolicy, before gens) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if before != (gens{e.epoch, e.userGens[userID]}) || !e.healthy.Load() {
		return false
	}
	e.users[userID] = u
	return true
}

func (e *Engine) invalidateTools() {
	e.mu.Lock()
	e.toolsGen++
	e.tools = nil
	e.mu.Unlock()
	e.notifyInvalidated()
}

// OnInvalidate registers fn to run whenever the tool or server catalog may have changed: a
// tools/upstream_servers notification, an unreadable payload, or the LISTEN connection being
// established or lost (notifications may have been missed). It lets other caches reuse this
// subscription instead of opening their own. fn runs on the listener goroutine, so it must be
// quick and must not block.
func (e *Engine) OnInvalidate(fn func()) {
	e.hooksMu.Lock()
	defer e.hooksMu.Unlock()
	e.hooks = append(e.hooks, fn)
}

// OnServerChanged registers fn to run when an upstream_servers row changes (status, slug, sealed
// stdio environment) with that row's id, or with "" when notifications may have been missed (the
// LISTEN connection was established or lost, or a payload was unreadable). Same constraints as
// OnInvalidate: fn runs on the listener goroutine.
func (e *Engine) OnServerChanged(fn func(serverID string)) {
	e.hooksMu.Lock()
	defer e.hooksMu.Unlock()
	e.serverHooks = append(e.serverHooks, fn)
}

func (e *Engine) notifyServerChanged(serverID string) {
	e.hooksMu.Lock()
	hooks := slices.Clone(e.serverHooks)
	e.hooksMu.Unlock()
	for _, fn := range hooks {
		fn(serverID)
	}
}

func (e *Engine) notifyInvalidated() {
	e.hooksMu.Lock()
	hooks := slices.Clone(e.hooks)
	e.hooksMu.Unlock()
	for _, fn := range hooks {
		fn()
	}
}

func (e *Engine) invalidateUser(userID string) {
	e.mu.Lock()
	e.userGens[userID]++
	delete(e.users, userID)
	e.mu.Unlock()
}

func (e *Engine) flushAll() {
	e.mu.Lock()
	e.epoch++
	e.tools = nil
	clear(e.users)
	e.mu.Unlock()
	e.notifyInvalidated()
	e.notifyServerChanged("")
}
