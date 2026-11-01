package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/store"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

// Approve makes pending tools of a server active with their current definition, which becomes
// the approved one. tool selects one tool; "" approves every pending tool of the server. It
// returns the approved names. Quarantined tools are not approvable: they are absent upstream.
func Approve(ctx context.Context, db store.DB, serverID, tool string) ([]string, error) {
	rows, err := db.Query(ctx, `
		UPDATE tools SET status = 'active', approved_hash = schema_hash, pending_changes = '{}', updated_at = now()
		WHERE server_id = $1 AND status = 'pending_approval' AND ($2 = '' OR name = $2)
		RETURNING name`, serverID, tool)
	if err != nil {
		return nil, fmt.Errorf("approve tools: %w", err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("approve tools: %w", err)
	}
	return names, nil
}

// ApproveHash is Approve for one tool bound to the definition the admin reviewed: it only
// approves when the current schema_hash starts with hashPrefix (lowercase hex, validated by the
// caller), checked in the same statement as the update so a concurrent sync cannot slip a
// different definition in. It returns whether the tool was approved.
func ApproveHash(ctx context.Context, db store.DB, serverID, tool, hashPrefix string) (bool, error) {
	tag, err := db.Exec(ctx, `
		UPDATE tools SET status = 'active', approved_hash = schema_hash, pending_changes = '{}', updated_at = now()
		WHERE server_id = $1 AND name = $2 AND status = 'pending_approval' AND starts_with(schema_hash, $3)`,
		serverID, tool, hashPrefix)
	if err != nil {
		return false, fmt.Errorf("approve tool: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// Reclassify recomputes trust_level for the tools of a server from their stored definition and
// the server's current annotation trust. Admin-set levels are kept. It returns the changed names.
func Reclassify(ctx context.Context, db store.DB, serverID string) ([]string, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	trust, err := lockServer(ctx, tx, serverID)
	if err != nil {
		return nil, err
	}
	levels, err := storedLevels(ctx, tx, serverID, trust.annotations)
	if err != nil {
		return nil, err
	}
	var changed []string
	for name, level := range levels {
		tag, err := tx.Exec(ctx, `UPDATE tools SET trust_level = $3, updated_at = now()
			WHERE server_id = $1 AND name = $2 AND trust_level <> $3`, serverID, name, string(level))
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() > 0 {
			changed = append(changed, name)
		}
	}
	slices.Sort(changed)
	return changed, tx.Commit(ctx)
}

// storedLevels classifies the stored definitions of the tools whose level was not set by hand.
func storedLevels(ctx context.Context, tx pgx.Tx, serverID string, trustAnnotations bool) (map[string]Level, error) {
	rows, err := tx.Query(ctx, `SELECT name, annotations::text FROM tools
		WHERE server_id = $1 AND NOT trust_level_overridden FOR UPDATE`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	levels := map[string]Level{}
	for rows.Next() {
		var def ToolDef
		var annotations string
		if err := rows.Scan(&def.Name, &annotations); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(annotations), &def.Annotations) != nil {
			def.Annotations = upstream.Annotations{} // classify by name only: never lowers the level
		}
		levels[def.Name] = Classify(def, trustAnnotations)
	}
	return levels, rows.Err()
}
