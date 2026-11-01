package policy

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type toolKey struct{ serverSlug, name string }

type toolInfo struct {
	serverID string
	level    Level
	active   bool
}

type toolRow struct {
	key  toolKey
	info toolInfo
}

// toolSet is an immutable snapshot of the tools of active servers.
type toolSet struct {
	byKey map[toolKey]toolInfo
	rows  []toolRow // sorted, for AllowedTools
}

type consentKey struct{ clientID, serverID string }

type consentInfo struct {
	level     Level
	expiresAt time.Time // zero = never expires
}

// userPolicy is an immutable snapshot of one user's ceilings and non-revoked consents.
type userPolicy struct {
	ceilings map[string]Level // by server id
	consents map[consentKey]consentInfo
}

func loadTools(ctx context.Context, pool *pgxpool.Pool) (*toolSet, error) {
	rows, err := pool.Query(ctx, `
		SELECT s.id::text, s.slug, t.name, t.trust_level, t.status
		FROM tools t JOIN upstream_servers s ON s.id = t.server_id
		WHERE s.status = 'active'`)
	if err != nil {
		return nil, fmt.Errorf("policy: load tools: %w", err)
	}
	defer rows.Close()

	set := &toolSet{byKey: make(map[toolKey]toolInfo)}
	for rows.Next() {
		var serverID, slug, name, level, status string
		if err := rows.Scan(&serverID, &slug, &name, &level, &status); err != nil {
			return nil, fmt.Errorf("policy: scan tool: %w", err)
		}
		lvl, ok := ParseLevel(level)
		if !ok {
			continue // unknown level: leave the tool out, so it is denied as unknown
		}
		r := toolRow{toolKey{slug, name}, toolInfo{serverID, lvl, status == "active"}}
		set.byKey[r.key] = r.info
		set.rows = append(set.rows, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("policy: load tools: %w", err)
	}
	sort.Slice(set.rows, func(i, j int) bool {
		a, b := set.rows[i].key, set.rows[j].key
		return a.serverSlug < b.serverSlug || (a.serverSlug == b.serverSlug && a.name < b.name)
	})
	return set, nil
}

// CeilingOf reads the user's trust ceiling on a server straight from the database (read-only, no
// cache). It fails closed: no row, an unknown level or any error (including malformed ids) is LevelNone.
func CeilingOf(ctx context.Context, pool *pgxpool.Pool, userID, serverID string) Level {
	var level string
	err := pool.QueryRow(ctx, `SELECT max_level FROM trust_ceilings WHERE user_id = $1::uuid AND server_id = $2::uuid`,
		userID, serverID).Scan(&level)
	if err != nil {
		return LevelNone
	}
	lvl, _ := ParseLevel(level)
	return lvl
}

func loadUser(ctx context.Context, pool *pgxpool.Pool, userID string) (*userPolicy, error) {
	up := &userPolicy{ceilings: map[string]Level{}, consents: map[consentKey]consentInfo{}}

	crows, err := pool.Query(ctx, `SELECT server_id::text, max_level FROM trust_ceilings WHERE user_id = $1::uuid`, userID)
	if err != nil {
		return nil, fmt.Errorf("policy: load ceilings: %w", err)
	}
	for crows.Next() {
		var serverID, level string
		if err := crows.Scan(&serverID, &level); err != nil {
			crows.Close()
			return nil, fmt.Errorf("policy: scan ceiling: %w", err)
		}
		if lvl, ok := ParseLevel(level); ok {
			up.ceilings[serverID] = lvl
		}
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return nil, fmt.Errorf("policy: load ceilings: %w", err)
	}

	nrows, err := pool.Query(ctx, `
		SELECT client_id, server_id::text, level, expires_at
		FROM consents WHERE user_id = $1::uuid AND revoked_at IS NULL`, userID)
	if err != nil {
		return nil, fmt.Errorf("policy: load consents: %w", err)
	}
	defer nrows.Close()
	for nrows.Next() {
		var clientID, serverID, level string
		var expires *time.Time
		if err := nrows.Scan(&clientID, &serverID, &level, &expires); err != nil {
			return nil, fmt.Errorf("policy: scan consent: %w", err)
		}
		lvl, ok := ParseLevel(level)
		if !ok {
			continue
		}
		info := consentInfo{level: lvl}
		if expires != nil {
			info.expiresAt = *expires
		}
		up.consents[consentKey{clientID, serverID}] = info
	}
	if err := nrows.Err(); err != nil {
		return nil, fmt.Errorf("policy: load consents: %w", err)
	}
	return up, nil
}
