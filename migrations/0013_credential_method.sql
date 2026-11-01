-- How a connection authenticates to its server. An oauth server may also accept a pasted token
-- (upstream_servers.header_name set), and each connection picks one. '' is legacy: read it as the
-- server's auth_kind (oauth -> oauth, header -> token).
ALTER TABLE upstream_credentials
    ADD COLUMN method text NOT NULL DEFAULT '',
    ADD CONSTRAINT upstream_credentials_method CHECK (method IN ('', 'oauth', 'token'));
