-- Per-connection environment variables of a stdio server with a per-user credential. The admin
-- declares the names (connection_env); each connection (personal profile row or shared row) stores
-- its own values sealed in env_ciphertext (JSON object name -> value, AAD = the row's credential
-- AAD + ':env'). At spawn a connection value overrides the fixed variable of the same name, which
-- therefore acts as the default; the token (credential_env) is applied last.

-- True when every element is a valid, distinct environment variable name.
CREATE FUNCTION valid_env_names(names text[]) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
    SELECT coalesce(bool_and(n IS NOT NULL AND n ~ '^[A-Z_][A-Z0-9_]{0,63}$'), true)
        AND count(*) = count(DISTINCT n)
    FROM unnest(names) AS n
$$;

ALTER TABLE upstream_servers
    ADD COLUMN connection_env text[] NOT NULL DEFAULT '{}',
    ADD CONSTRAINT upstream_servers_connection_env_names
        CHECK (cardinality(connection_env) <= 16 AND valid_env_names(connection_env)),
    -- Only a server that takes a per-user token has connections to vary, and the token variable
    -- itself is never a per-connection value.
    ADD CONSTRAINT upstream_servers_connection_env_credential CHECK (
        cardinality(connection_env) = 0
        OR (credential_env IS NOT NULL AND NOT credential_env = ANY (connection_env)));

ALTER TABLE upstream_credentials
    ADD COLUMN env_ciphertext bytea,
    ADD CONSTRAINT upstream_credentials_inactive_no_env CHECK (status = 'active' OR env_ciphertext IS NULL);
