-- Runs after the CLI seed (users, server fake, catalog sync, ceilings). Idempotent per fresh database.
INSERT INTO oauth_clients (client_id, registration_kind, metadata, redirect_uris)
VALUES ('e2e-agent', 'dcr', '{"client_name":"E2E Agent"}', ARRAY['http://127.0.0.1:9/callback']);

INSERT INTO consents (user_id, client_id, server_id, level)
SELECT u.id, 'e2e-agent', s.id, 'read'
FROM users u, upstream_servers s
WHERE u.email = 'ana@example.com' AND s.slug = 'fake';

UPDATE tools SET status = 'pending_approval', pending_changes = '{schema}'
WHERE name = 'get_item' AND server_id = (SELECT id FROM upstream_servers WHERE slug = 'fake');
