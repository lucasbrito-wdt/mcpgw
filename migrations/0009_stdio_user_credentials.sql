-- Per-user credentials for stdio servers. A stdio server with auth_kind 'header' keeps its tokens in
-- upstream_credentials exactly like an HTTP header server (portal flow, profiles, shared row, AAD);
-- only the application differs: the resolved token is set as the environment variable named
-- credential_env (raw, without header_prefix) on top of the sealed fixed environment.

ALTER TABLE upstream_servers
    ADD COLUMN credential_env text CHECK (credential_env ~ '^[A-Z_][A-Z0-9_]{0,63}$'),
    DROP CONSTRAINT upstream_servers_stdio_auth,
    -- stdio: either no credential at all, or a per-user token delivered through credential_env.
    ADD CONSTRAINT upstream_servers_stdio_auth CHECK (
        transport <> 'stdio'
        OR (auth_kind = 'none' AND credential_env IS NULL)
        OR (auth_kind = 'header' AND credential_env IS NOT NULL)),
    ADD CONSTRAINT upstream_servers_credential_env_stdio CHECK (transport = 'stdio' OR credential_env IS NULL);
