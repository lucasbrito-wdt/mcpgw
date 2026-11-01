-- Phase-1 schema. Postgres 18 (built-in uuidv7()). Enums are text + CHECK.
-- Trust levels, ordered: read < write < destructive.

CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    email         text NOT NULL UNIQUE CHECK (email = lower(email)),
    password_hash text NOT NULL,
    is_admin      boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE oauth_clients (
    id                uuid PRIMARY KEY DEFAULT uuidv7(),
    -- CIMD: the metadata document URL. DCR: a generated id.
    client_id         text NOT NULL UNIQUE,
    registration_kind text NOT NULL CHECK (registration_kind IN ('cimd', 'dcr')),
    metadata          jsonb NOT NULL DEFAULT '{}'::jsonb,
    redirect_uris     text[] NOT NULL DEFAULT '{}',
    -- Last time a CIMD document was fetched (cache freshness); NULL for DCR.
    fetched_at        timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE oauth_authorization_codes (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    -- Only the hash of the code is stored.
    code_hash      text NOT NULL UNIQUE,
    client_id      text NOT NULL REFERENCES oauth_clients (client_id) ON DELETE CASCADE,
    user_id        uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    redirect_uri   text NOT NULL,
    code_challenge text NOT NULL,
    scope          text NOT NULL DEFAULT '',
    -- RFC 8707 resource indicator the token will be bound to.
    resource       text NOT NULL,
    expires_at     timestamptz NOT NULL,
    used_at        timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX oauth_authorization_codes_expires_at_idx ON oauth_authorization_codes (expires_at);

CREATE TABLE oauth_refresh_tokens (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    token_hash text NOT NULL UNIQUE,
    -- All tokens descended from one authorization share a family; reuse of a used token revokes the family.
    family_id  uuid NOT NULL,
    -- The token this one was rotated from; kept as history if the parent row is pruned.
    parent_id  uuid REFERENCES oauth_refresh_tokens (id) ON DELETE SET NULL,
    client_id  text NOT NULL REFERENCES oauth_clients (client_id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    scope      text NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX oauth_refresh_tokens_family_id_idx ON oauth_refresh_tokens (family_id);
CREATE INDEX oauth_refresh_tokens_user_id_idx ON oauth_refresh_tokens (user_id);

CREATE TABLE signing_keys (
    id                     uuid PRIMARY KEY DEFAULT uuidv7(),
    kid                    text NOT NULL UNIQUE,
    alg                    text NOT NULL,
    -- Encrypted with the vault master key.
    private_key_ciphertext bytea NOT NULL,
    public_jwk             jsonb NOT NULL,
    created_at             timestamptz NOT NULL DEFAULT now(),
    -- Retired keys stay published in the JWKS until issued tokens expire.
    retired_at             timestamptz
);

CREATE TABLE upstream_servers (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    -- Namespace used in the generated code API, so it must be a safe identifier.
    slug       text NOT NULL UNIQUE CHECK (slug ~ '^[a-z][a-z0-9_]{0,62}$'),
    url        text NOT NULL,
    auth_kind  text NOT NULL CHECK (auth_kind IN ('none', 'oauth', 'static_header')),
    status     text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE upstream_credentials (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    server_id          uuid NOT NULL REFERENCES upstream_servers (id) ON DELETE CASCADE,
    user_id            uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    access_ciphertext  bytea NOT NULL,
    refresh_ciphertext bytea,
    -- Identifies the master key version used, to allow rotation.
    key_id             text NOT NULL,
    expires_at         timestamptz,
    scope              text NOT NULL DEFAULT '',
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (server_id, user_id)
);

CREATE TABLE upstream_oauth_states (
    id                       uuid PRIMARY KEY DEFAULT uuidv7(),
    state                    text NOT NULL UNIQUE,
    server_id                uuid NOT NULL REFERENCES upstream_servers (id) ON DELETE CASCADE,
    user_id                  uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_verifier_ciphertext bytea NOT NULL,
    expires_at               timestamptz NOT NULL,
    created_at               timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX upstream_oauth_states_expires_at_idx ON upstream_oauth_states (expires_at);

CREATE TABLE tools (
    id                     uuid PRIMARY KEY DEFAULT uuidv7(),
    server_id              uuid NOT NULL REFERENCES upstream_servers (id) ON DELETE CASCADE,
    name                   text NOT NULL,
    description            text NOT NULL DEFAULT '',
    input_schema           jsonb NOT NULL DEFAULT '{}'::jsonb,
    output_schema          jsonb,
    annotations            jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- Hash of name + description + schemas, for change detection (pinning, phase 2).
    schema_hash            text NOT NULL,
    trust_level            text NOT NULL CHECK (trust_level IN ('read', 'write', 'destructive')),
    -- True when an admin set trust_level by hand; refresh must not overwrite it.
    trust_level_overridden boolean NOT NULL DEFAULT false,
    status                 text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'quarantined')),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    UNIQUE (server_id, name)
);

-- Admin-set maximum trust level per user and server.
CREATE TABLE trust_ceilings (
    user_id   uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    server_id uuid NOT NULL REFERENCES upstream_servers (id) ON DELETE CASCADE,
    max_level text NOT NULL CHECK (max_level IN ('read', 'write', 'destructive')),
    PRIMARY KEY (user_id, server_id)
);

-- User consent given to an OAuth client for a server at a level (effective = min(ceiling, consent)).
CREATE TABLE consents (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client_id  text NOT NULL REFERENCES oauth_clients (client_id) ON DELETE CASCADE,
    server_id  uuid NOT NULL REFERENCES upstream_servers (id) ON DELETE CASCADE,
    level      text NOT NULL CHECK (level IN ('read', 'write', 'destructive')),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    revoked_at timestamptz,
    UNIQUE (user_id, client_id, server_id)
);

-- No foreign keys on executions and audit_events: audit rows must outlive users, clients and servers.
CREATE TABLE executions (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id     uuid NOT NULL,
    client_id   text NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('search', 'execute')),
    code        text NOT NULL,
    code_hash   text NOT NULL,
    status      text NOT NULL DEFAULT 'running'
                CHECK (status IN ('running', 'succeeded', 'failed', 'timed_out', 'cancelled')),
    inner_calls integer NOT NULL DEFAULT 0,
    error       text,
    started_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);
CREATE INDEX executions_user_id_started_at_idx ON executions (user_id, started_at DESC);

-- Partitioned by month on ts. The primary key must include the partition key.
CREATE TABLE audit_events (
    id            uuid NOT NULL DEFAULT uuidv7(),
    ts            timestamptz NOT NULL DEFAULT now(),
    execution_id  uuid,
    user_id       uuid NOT NULL,
    client_id     text NOT NULL,
    server_slug   text NOT NULL,
    tool_name     text NOT NULL,
    decision      text NOT NULL CHECK (decision IN ('allow', 'deny')),
    reason        text NOT NULL DEFAULT '',
    args_hash     text NOT NULL DEFAULT '',
    args_redacted jsonb,
    latency_ms    integer,
    result_bytes  integer,
    PRIMARY KEY (id, ts)
) PARTITION BY RANGE (ts);

-- Indexes on the parent are created on every partition, present and future.
CREATE INDEX audit_events_user_id_ts_idx ON audit_events (user_id, ts);
CREATE INDEX audit_events_execution_id_idx ON audit_events (execution_id);

-- Catches rows outside any monthly partition. A monthly partition cannot be created
-- while the default partition holds rows for that month, so create partitions ahead of time.
CREATE TABLE audit_events_default PARTITION OF audit_events DEFAULT;

-- Creates the partition covering the month of p_month (UTC) if missing; returns its name.
CREATE FUNCTION create_audit_partition(p_month date) RETURNS text
LANGUAGE plpgsql AS $$
DECLARE
    v_start date := date_trunc('month', p_month)::date;
    v_name  text := format('audit_events_%s', to_char(v_start, 'YYYY_MM'));
BEGIN
    EXECUTE format(
        'CREATE TABLE IF NOT EXISTS %I PARTITION OF audit_events FOR VALUES FROM (%L) TO (%L)',
        v_name,
        format('%s 00:00:00+00', v_start),
        format('%s 00:00:00+00', (v_start + interval '1 month')::date));
    RETURN v_name;
END;
$$;

SELECT create_audit_partition(current_date);
SELECT create_audit_partition((current_date + interval '1 month')::date);

-- Policy cache invalidation: listeners LISTEN on 'policy_changed'.
-- Payload: {"table":..., "op":..., "user_id":..., "server_id":...} (ids may be null).
CREATE FUNCTION notify_policy_changed() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    r jsonb := to_jsonb(CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END);
BEGIN
    PERFORM pg_notify('policy_changed', jsonb_build_object(
        'table', TG_TABLE_NAME,
        'op', TG_OP,
        'user_id', r ->> 'user_id',
        'server_id', r ->> 'server_id')::text);
    RETURN NULL;
END;
$$;

CREATE TRIGGER trust_ceilings_policy_changed
    AFTER INSERT OR UPDATE OR DELETE ON trust_ceilings
    FOR EACH ROW EXECUTE FUNCTION notify_policy_changed();

CREATE TRIGGER consents_policy_changed
    AFTER INSERT OR UPDATE OR DELETE ON consents
    FOR EACH ROW EXECUTE FUNCTION notify_policy_changed();

-- For tools, only policy-relevant columns notify (catalog refreshes touch updated_at and schemas often).
CREATE TRIGGER tools_policy_changed
    AFTER INSERT OR DELETE OR UPDATE OF trust_level, trust_level_overridden, status ON tools
    FOR EACH ROW EXECUTE FUNCTION notify_policy_changed();
