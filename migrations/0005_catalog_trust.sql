-- Catalog trust (W6): per-server annotation trust and tool pinning (trust on first sync, then
-- every new or changed definition waits for an admin approval).

ALTER TABLE upstream_servers
    -- Annotations are untrusted hints: readOnlyHint may lower a tool's level only when true.
    ADD COLUMN trust_annotations boolean NOT NULL DEFAULT false,
    -- Tools that appear after the first sync are active at once instead of pending approval.
    ADD COLUMN auto_approve_new_tools boolean NOT NULL DEFAULT false,
    -- Last successful catalog sync; NULL means the next sync is the first one (trust on first use).
    ADD COLUMN catalog_synced_at timestamptz;

-- Servers synced before this migration already had their catalog trusted.
UPDATE upstream_servers s SET catalog_synced_at = now()
WHERE EXISTS (SELECT 1 FROM tools t WHERE t.server_id = s.id);

ALTER TABLE tools
    DROP CONSTRAINT tools_status_check,
    ADD CONSTRAINT tools_status_check CHECK (status IN ('active', 'quarantined', 'pending_approval')),
    -- schema_hash of the definition an admin (or the first sync) approved; NULL for a new tool
    -- never approved. A listed definition with another hash is pending approval.
    ADD COLUMN approved_hash text,
    -- What changed since the last approved definition, for `admin tool list`: any of
    -- 'new', 'description', 'schema', 'annotations'. Empty when nothing is pending.
    ADD COLUMN pending_changes text[] NOT NULL DEFAULT '{}';

UPDATE tools SET approved_hash = schema_hash;

COMMENT ON COLUMN tools.schema_hash IS
    'sha256 of canonical {name, description, input_schema, output_schema, annotations}';
