-- Per-connection upstream address. With connection_url on (header servers over HTTP only), each
-- connection names its own MCP URL in upstream_credentials.url, used instead of
-- upstream_servers.url. A connection without one (created before the switch) keeps using the
-- server's URL. The URL is not secret and is stored in clear.
ALTER TABLE upstream_servers
    ADD COLUMN connection_url boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT upstream_servers_connection_url CHECK (
        NOT connection_url OR (auth_kind = 'header' AND transport <> 'stdio'));

ALTER TABLE upstream_credentials
    ADD COLUMN url text,
    ADD CONSTRAINT upstream_credentials_url_length CHECK (char_length(url) BETWEEN 1 AND 2048);

-- Switching connection_url changes where calls go: running gateways must hear about it.
DROP TRIGGER upstream_servers_policy_changed ON upstream_servers;

CREATE TRIGGER upstream_servers_policy_changed
    AFTER INSERT OR DELETE OR UPDATE OF status, slug, stdio_env_ciphertext, credential_env, auth_kind, connection_url
    ON upstream_servers
    FOR EACH ROW EXECUTE FUNCTION notify_upstream_server_changed();
