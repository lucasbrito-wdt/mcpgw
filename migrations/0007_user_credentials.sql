-- Credentials (phase 2), EXPAND step. auth_kind 'header' replaces 'static_header': the server
-- declares the header the upstream accepts; the value comes from a connection, either personal
-- (one per server and user) or shared (one per server, admin-created, used by every user without
-- a personal one). 'static_header' stays valid until the contract migration, which also drops
-- static_header_name/static_header_ciphertext after a Go step re-sealed them into shared rows.

ALTER TABLE upstream_servers
    DROP CONSTRAINT upstream_servers_auth_kind_check,
    ADD CONSTRAINT upstream_servers_auth_kind_check
        CHECK (auth_kind IN ('none', 'oauth', 'header', 'static_header')),
    ADD COLUMN header_name text CHECK (header_name ~ '^[A-Za-z0-9-]{1,100}$'),
    -- Prepended to the connection's token, e.g. 'Bearer '. Printable ASCII only (it goes into a header).
    ADD COLUMN header_prefix text NOT NULL DEFAULT '' CHECK (header_prefix ~ '^[\x20-\x7E]{0,32}$'),
    -- Expected start of the token (e.g. 'github_pat_'); the portal rejects a token without it.
    ADD COLUMN header_format_hint text CHECK (header_format_hint ~ '^[\x21-\x7E]{1,64}$'),
    ADD COLUMN header_help_url text
        CHECK (header_help_url ~ '^https://' AND char_length(header_help_url) <= 2048),
    -- Portal grouping and filter (e.g. 'dev', 'docs', 'crm').
    ADD COLUMN category text CHECK (category ~ '^[a-z][a-z0-9_-]{0,31}$');

-- The header name is not secret: copy it now; the sealed value needs the Go re-seal step.
UPDATE upstream_servers SET header_name = static_header_name WHERE static_header_name IS NOT NULL;

ALTER TABLE upstream_credentials
    ADD COLUMN shared boolean NOT NULL DEFAULT false,
    -- Shared rows belong to no user (user_id NULL, so UNIQUE (server_id, user_id) ignores them);
    -- connected_by is the admin who created it, NULL for the CLI and the legacy migration.
    ALTER COLUMN user_id DROP NOT NULL,
    ADD COLUMN connected_by uuid REFERENCES users (id) ON DELETE SET NULL,
    ADD CONSTRAINT upstream_credentials_shared_has_no_user CHECK (shared = (user_id IS NULL)),
    -- Revoked or refresh-failed rows keep status and reason but no secret.
    ALTER COLUMN access_ciphertext DROP NOT NULL,
    ADD COLUMN account_label text CHECK (char_length(account_label) <= 200),
    -- Last 4 characters of a header token, shown as ••••last4. Never for OAuth tokens.
    ADD COLUMN last4 text CHECK (char_length(last4) BETWEEN 1 AND 4),
    ADD COLUMN last_used_at timestamptz,
    ADD COLUMN status text NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'refresh_failed', 'revoked_by_admin')),
    ADD COLUMN revoked_reason text CHECK (char_length(revoked_reason) <= 500),
    ADD CONSTRAINT upstream_credentials_active_has_token
        CHECK (status <> 'active' OR access_ciphertext IS NOT NULL),
    ADD CONSTRAINT upstream_credentials_inactive_no_secret
        CHECK (status = 'active' OR (access_ciphertext IS NULL AND refresh_ciphertext IS NULL));

CREATE UNIQUE INDEX upstream_credentials_shared_key ON upstream_credentials (server_id) WHERE shared;
-- The portal lists one user's credentials across up to ~80 servers.
CREATE INDEX upstream_credentials_user_id_idx ON upstream_credentials (user_id);

ALTER TABLE upstream_oauth_states ADD COLUMN shared boolean NOT NULL DEFAULT false;
