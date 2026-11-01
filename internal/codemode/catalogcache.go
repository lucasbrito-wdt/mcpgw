package codemode

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lucasbrito-wdt/mcpgw/internal/catalog"
)

const (
	defaultCatalogTTL = 30 * time.Second
	maxDeclCacheSize  = 256
)

type serverInfo struct{ id, url, authKind string }

// snapshot is an immutable view of the active tools of active servers, plus a small cache of the
// declarations generated from it (a new snapshot starts with an empty one).
type snapshot struct {
	loaded      time.Time
	gen         uint64 // catalogCache generation observed before loading
	byQualified map[string]catalog.ToolDef
	servers     map[string]serverInfo
	idents      map[string]*catalog.IdentMap
	// inactive holds the qualified raw names of tools that exist but are not active (quarantined
	// or disabled, or on an inactive server). They are not callable, and a script that names one
	// must be denied rather than resolved to another tool through the identifier map.
	inactive map[string]struct{}

	mu   sync.Mutex
	decl map[string]SearchResult
}

func (s *snapshot) cachedSearch(key string) (SearchResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.decl[key]
	return r, ok
}

func (s *snapshot) storeSearch(key string, r SearchResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.decl) >= maxDeclCacheSize {
		clear(s.decl)
	}
	s.decl[key] = r
}

// isInactive reports whether the exact raw name is a known tool that is not active.
func (s *snapshot) isInactive(server, tool string) bool {
	_, ok := s.inactive[catalog.QualifiedName(server, tool)]
	return ok
}

// resolveTool maps the name a script used to the raw upstream tool name. The generated
// declarations expose sanitized identifiers (get-item becomes get_item), so a script may use
// either form. ok is false when the raw-name match and the identifier match point to different
// tools: the name is ambiguous and must not be run. The idents maps are built from the full
// tool set and never yield another tool's raw name, so this only fires on an inconsistent
// snapshot. The result is always re-authorized by policy.
func (s *snapshot) resolveTool(server, tool string) (raw string, ok bool) {
	_, rawMatch := s.byQualified[catalog.QualifiedName(server, tool)]
	var identRaw string
	var identMatch bool
	if m := s.idents[server]; m != nil {
		identRaw, identMatch = m.Name(tool)
	}
	switch {
	case rawMatch && identMatch && identRaw != tool:
		return tool, false
	case rawMatch:
		return tool, true
	case identMatch:
		return identRaw, true
	}
	return tool, true
}

// catalogCache loads the catalog from the database and keeps it for a short TTL.
//
// The TTL is a backstop: invalidate (wired to the policy LISTEN subscription) drops the snapshot
// as soon as a tool or server status changes. Policy is checked against its own state on every
// call, so a stale catalog can only show the model a stale type or hide a new tool; it can never
// allow a call. Changes the database does not notify (a description or schema rewritten in
// place, a server URL change) still wait for the TTL.
type catalogCache struct {
	pool *pgxpool.Pool
	ttl  time.Duration
	now  func() time.Time

	gen atomic.Uint64 // bumped by invalidate

	mu   sync.Mutex
	snap *snapshot
}

func newCatalogCache(pool *pgxpool.Pool, ttl time.Duration) *catalogCache {
	if ttl <= 0 {
		ttl = defaultCatalogTTL
	}
	return &catalogCache{pool: pool, ttl: ttl, now: time.Now}
}

func (c *catalogCache) get(ctx context.Context) (*snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snap != nil && c.snap.gen == c.gen.Load() && c.now().Sub(c.snap.loaded) < c.ttl {
		return c.snap, nil
	}
	return c.reloadLocked(ctx)
}

// invalidate makes the next get reload. It never blocks, so it is safe in a LISTEN callback. A
// load already in flight carries the older generation and is not reused after it returns.
func (c *catalogCache) invalidate() { c.gen.Add(1) }

// refresh forces a reload, for a call that policy allowed but the snapshot does not know.
func (c *catalogCache) refresh(ctx context.Context) (*snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reloadLocked(ctx)
}

func (c *catalogCache) reloadLocked(ctx context.Context) (*snapshot, error) {
	gen := c.gen.Load()
	snap, err := loadSnapshot(ctx, c.pool, c.now())
	if err != nil {
		return nil, err
	}
	snap.gen = gen
	c.snap = snap
	return snap, nil
}

func loadSnapshot(ctx context.Context, pool *pgxpool.Pool, now time.Time) (*snapshot, error) {
	rows, err := pool.Query(ctx, `
		SELECT s.id::text, s.slug, s.url, s.auth_kind, t.name, t.description,
		       t.input_schema::text, COALESCE(t.output_schema::text, ''), t.annotations::text
		FROM tools t JOIN upstream_servers s ON s.id = t.server_id
		WHERE s.status = 'active' AND t.status = 'active'`)
	if err != nil {
		return nil, fmt.Errorf("codemode: load catalog: %w", err)
	}
	defer rows.Close()

	snap := &snapshot{
		loaded:      now,
		byQualified: map[string]catalog.ToolDef{},
		servers:     map[string]serverInfo{},
		idents:      map[string]*catalog.IdentMap{},
		decl:        map[string]SearchResult{},
		inactive:    map[string]struct{}{},
	}
	names := map[string][]string{}
	for rows.Next() {
		var id, slug, url, authKind, annotations string
		var def catalog.ToolDef
		var input, output string
		if err := rows.Scan(&id, &slug, &url, &authKind, &def.Name, &def.Description, &input, &output, &annotations); err != nil {
			return nil, fmt.Errorf("codemode: scan catalog: %w", err)
		}
		def.Server, def.InputSchema = slug, json.RawMessage(input)
		if output != "" {
			def.OutputSchema = json.RawMessage(output)
		}
		_ = json.Unmarshal([]byte(annotations), &def.Annotations) // best effort: annotations are hints only
		def.ProfileArg = catalog.ProfileArg(authKind, def.InputSchema)
		snap.byQualified[catalog.QualifiedName(slug, def.Name)] = def
		snap.servers[slug] = serverInfo{id: id, url: url, authKind: authKind}
		names[slug] = append(names[slug], def.Name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("codemode: load catalog: %w", err)
	}
	rows.Close()
	for slug, n := range names {
		snap.idents[slug] = catalog.NewIdentMap(n)
	}
	if err := loadInactive(ctx, pool, snap); err != nil {
		return nil, err
	}
	return snap, nil
}

func loadInactive(ctx context.Context, pool *pgxpool.Pool, snap *snapshot) error {
	rows, err := pool.Query(ctx, `
		SELECT s.slug, t.name FROM tools t JOIN upstream_servers s ON s.id = t.server_id
		WHERE NOT (s.status = 'active' AND t.status = 'active')`)
	if err != nil {
		return fmt.Errorf("codemode: load inactive tools: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var slug, name string
		if err := rows.Scan(&slug, &name); err != nil {
			return fmt.Errorf("codemode: scan inactive tools: %w", err)
		}
		snap.inactive[catalog.QualifiedName(slug, name)] = struct{}{}
	}
	return rows.Err()
}
