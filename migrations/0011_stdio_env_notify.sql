-- Editing the sealed fixed environment of a stdio server must reach running gateways, so they can
-- restart the pooled processes: stdio_env_ciphertext joins the columns that fire the notification.
DROP TRIGGER upstream_servers_policy_changed ON upstream_servers;

CREATE TRIGGER upstream_servers_policy_changed
    AFTER INSERT OR DELETE OR UPDATE OF status, slug, stdio_env_ciphertext ON upstream_servers
    FOR EACH ROW EXECUTE FUNCTION notify_upstream_server_changed();
