-- Switching a stdio server to a per-user credential (credential_env, auth_kind) must reach running
-- gateways, so they restart the pooled processes started with the old credential setup.
DROP TRIGGER upstream_servers_policy_changed ON upstream_servers;

CREATE TRIGGER upstream_servers_policy_changed
    AFTER INSERT OR DELETE OR UPDATE OF status, slug, stdio_env_ciphertext, credential_env, auth_kind ON upstream_servers
    FOR EACH ROW EXECUTE FUNCTION notify_upstream_server_changed();
