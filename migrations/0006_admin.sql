-- Admin web (phase 2): who did what through /admin/api (and admin acts in /portal/api, such as
-- shared connections). One row per successful write, inserted in the same transaction as the
-- write. details never holds secret values (header values, client secrets, passwords, tokens).
-- No foreign keys: audit rows outlive users.
CREATE TABLE admin_events (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    ts            timestamptz NOT NULL DEFAULT now(),
    actor_user_id uuid NOT NULL,
    action        text NOT NULL CHECK (action ~ '^[a-z_]+\.[a-z_]+$'),
    target        text NOT NULL DEFAULT '' CHECK (char_length(target) <= 300),
    details       jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details) = 'object'),
    ip            text NOT NULL DEFAULT ''
);
-- Keyset pagination is on id (uuidv7, time ordered); the actor filter needs its own index.
CREATE INDEX admin_events_actor_id_idx ON admin_events (actor_user_id, id DESC);
CREATE INDEX admin_events_target_idx ON admin_events (target, id DESC);
