-- Upstream auth (W2): policy invalidation for upstream_servers, static credentials,
-- OAuth client registration cache and the extra facts the connect flow must remember.

-- upstream_servers has no server_id column (its key is id), so it gets its own notify function.
-- Same payload shape as notify_policy_changed(); server_id carries the row id.
CREATE FUNCTION notify_upstream_server_changed() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    r jsonb := to_jsonb(CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END);
BEGIN
    PERFORM pg_notify('policy_changed', jsonb_build_object(
        'table', TG_TABLE_NAME,
        'op', TG_OP,
        'user_id', NULL,
        'server_id', r ->> 'id')::text);
    RETURN NULL;
END;
$$;

CREATE TRIGGER upstream_servers_policy_changed
    AFTER INSERT OR DELETE OR UPDATE OF status, slug ON upstream_servers
    FOR EACH ROW EXECUTE FUNCTION notify_upstream_server_changed();

-- /connect/callback and /connect/success are literal routes and would shadow these slugs.
ALTER TABLE upstream_servers
    ADD CONSTRAINT upstream_servers_slug_not_reserved CHECK (slug NOT IN ('callback', 'success'));

-- Admin-provided (pre-registered) OAuth client; the secret is sealed with the vault,
-- AAD "upstream_servers:<id>:oauth_client_secret".
ALTER TABLE upstream_servers
    ADD COLUMN oauth_client_id text,
    ADD COLUMN oauth_client_secret_ciphertext bytea,
    -- auth_kind = 'static_header': header name and sealed value,
    -- AAD "upstream_servers:<id>:static_header".
    ADD COLUMN static_header_name text CHECK (static_header_name ~ '^[A-Za-z0-9-]{1,100}$'),
    ADD COLUMN static_header_ciphertext bytea,
    ADD CONSTRAINT upstream_servers_static_header_pair
        CHECK ((static_header_name IS NULL) = (static_header_ciphertext IS NULL));

-- Client registrations obtained by DCR, per server and authorization server (avoids
-- re-registering on every connect). The secret, if any, is sealed with the vault,
-- AAD "upstream_oauth_clients:<server_id>:<issuer>".
CREATE TABLE upstream_oauth_clients (
    server_id                 uuid NOT NULL REFERENCES upstream_servers (id) ON DELETE CASCADE,
    issuer                    text NOT NULL,
    client_id                 text NOT NULL,
    client_secret_ciphertext  bytea,
    redirect_uri              text NOT NULL,
    created_at                timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (server_id, issuer)
);

-- Facts resolved when the flow starts; the callback trusts these, never a fresh discovery.
-- The state column stores the SHA-256 hex of the state value, not the value itself.
ALTER TABLE upstream_oauth_states
    ADD COLUMN issuer         text NOT NULL DEFAULT '',
    ADD COLUMN client_id      text NOT NULL DEFAULT '',
    ADD COLUMN token_endpoint text NOT NULL DEFAULT '',
    -- True when the authorization server advertised RFC 9207 support, making iss mandatory.
    ADD COLUMN iss_required   boolean NOT NULL DEFAULT false;

-- Same facts needed to refresh a grant against the server that issued it.
ALTER TABLE upstream_credentials
    ADD COLUMN issuer         text NOT NULL DEFAULT '',
    ADD COLUMN client_id      text NOT NULL DEFAULT '',
    ADD COLUMN token_endpoint text NOT NULL DEFAULT '';
