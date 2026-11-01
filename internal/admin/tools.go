package admin

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/catalog"
	"github.com/lucasbrito-wdt/mcpgw/internal/policy"
)

type Tool struct {
	ServerSlug, Name, Level string
	Overridden              bool
	Status                  string
	PendingChanges          []string
	Hash                    string
}

type ToolDetail struct {
	Tool
	Description, InputSchema, OutputSchema, Annotations, ApprovedHash string
}

type ToolFilter struct{ Server, Status string }

// ListTools returns the full schema_hash; the CLI prints its first 12 characters.
func (s *Service) ListTools(ctx context.Context, f ToolFilter) ([]Tool, error) {
	rows, err := s.o.Pool.Query(ctx, `SELECT s.slug, t.name, t.trust_level, t.trust_level_overridden, t.status, t.pending_changes, t.schema_hash
		FROM tools t JOIN upstream_servers s ON s.id = t.server_id
		WHERE ($1 = '' OR s.slug = $1) AND ($2 = '' OR t.status = $2)
		ORDER BY s.slug, t.name`, f.Server, f.Status)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (t Tool, err error) {
		err = r.Scan(&t.ServerSlug, &t.Name, &t.Level, &t.Overridden, &t.Status, &t.PendingChanges, &t.Hash)
		return
	})
}

func (s *Service) GetTool(ctx context.Context, slug, name string) (ToolDetail, error) {
	var d ToolDetail
	err := s.o.Pool.QueryRow(ctx, `SELECT s.slug, t.name, t.trust_level, t.trust_level_overridden, t.status, t.pending_changes, t.schema_hash,
		t.description, t.input_schema::text, COALESCE(t.output_schema::text, 'null'), t.annotations::text, COALESCE(t.approved_hash, '')
		FROM tools t JOIN upstream_servers s ON s.id = t.server_id WHERE s.slug = $1 AND t.name = $2`, slug, name).
		Scan(&d.ServerSlug, &d.Name, &d.Level, &d.Overridden, &d.Status, &d.PendingChanges, &d.Hash,
			&d.Description, &d.InputSchema, &d.OutputSchema, &d.Annotations, &d.ApprovedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, toolNotFound(slug, name)
	}
	return d, err
}

func toolNotFound(slug, name string) error {
	return notFound("tool not found: %s.%s (run catalog sync first)", "tool não encontrada: %s.%s (rode catalog sync antes)", slug, name)
}

// SetToolLevel pins the tool's level by hand (it survives later syncs).
func (s *Service) SetToolLevel(ctx context.Context, slug, name string, level policy.Level) error {
	if level == policy.LevelNone {
		return invalid("level", "level must be read, write or destructive", "o nível deve ser read, write ou destructive")
	}
	ev := Event{Action: "tool.set_level", Target: slug + "/" + name, Details: map[string]any{"level": level.String()}}
	return s.write(ctx, ev, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE tools SET trust_level = $3, trust_level_overridden = true, updated_at = now()
			WHERE name = $2 AND server_id = (SELECT id FROM upstream_servers WHERE slug = $1)`, slug, name, level.String())
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return toolNotFound(slug, name)
		}
		return nil
	})
}

// ApproveTool approves one pending tool. A non-empty hashPrefix (12+ hex of schema_hash) binds
// the approval to the definition the admin reviewed: *StaleHashError when it changed.
func (s *Service) ApproveTool(ctx context.Context, slug, name, hashPrefix string) ([]string, error) {
	if name == "" {
		return nil, invalid("tool", "provide the tool name", "informe o nome da tool")
	}
	prefix, err := NormalizeHashPrefix(hashPrefix)
	if err != nil {
		return nil, err
	}
	return s.approve(ctx, slug, name, prefix, "tool.approve", slug+"/"+name)
}

// ApproveAll approves every pending tool of the server; none pending returns an empty list.
func (s *Service) ApproveAll(ctx context.Context, slug string) ([]string, error) {
	return s.approve(ctx, slug, "", "", "tool.approve_all", slug)
}

func (s *Service) approve(ctx context.Context, slug, name, prefix, action, target string) ([]string, error) {
	var approved []string
	err := s.write(ctx, Event{Action: action, Target: target, Details: map[string]any{}}, func(tx pgx.Tx) error {
		srv, err := serverBySlug(ctx, tx, slug)
		if err != nil {
			return err
		}
		if prefix != "" {
			approved, err = approveHash(ctx, tx, srv.ID, slug, name, prefix)
		} else {
			approved, err = catalog.Approve(ctx, tx, srv.ID, name)
		}
		if err != nil {
			return err
		}
		if len(approved) == 0 && name != "" {
			return notPending(slug, name)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if approved == nil {
		approved = []string{}
	}
	return approved, nil
}

func notPending(slug, name string) error {
	return conflict("tool %s.%s is not awaiting approval (see tool list)", "tool %s.%s não está aguardando aprovação (veja tool list)", slug, name)
}

func approveHash(ctx context.Context, tx pgx.Tx, serverID, slug, name, prefix string) ([]string, error) {
	ok, err := catalog.ApproveHash(ctx, tx, serverID, name, prefix)
	if err != nil || ok {
		return []string{name}, err
	}
	var status, current string
	err = tx.QueryRow(ctx, `SELECT status, left(schema_hash, 12) FROM tools WHERE server_id = $1::uuid AND name = $2`, serverID, name).Scan(&status, &current)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, toolNotFound(slug, name)
	case err != nil:
		return nil, err
	case status != catalog.StatusPendingApproval:
		return nil, notPending(slug, name)
	}
	return nil, &StaleHashError{Slug: slug, Tool: name, Current: current, Given: prefix}
}
