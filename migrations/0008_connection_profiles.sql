-- Connection profiles: a user may hold several named personal connections to one server (e.g. one
-- per Cloudflare account). Existing rows become profile 'default', whose ciphertext AAD is unchanged
-- (no re-seal); other profiles bind the profile name into the AAD. Shared rows are always 'default'.

ALTER TABLE upstream_credentials
    ADD COLUMN profile text NOT NULL DEFAULT 'default' CHECK (profile ~ '^[a-z][a-z0-9_-]{0,31}$'),
    ADD CONSTRAINT upstream_credentials_shared_default_profile CHECK (NOT shared OR profile = 'default'),
    DROP CONSTRAINT upstream_credentials_server_id_user_id_key,
    -- Shared rows (user_id NULL) stay unique through the partial index upstream_credentials_shared_key.
    ADD CONSTRAINT upstream_credentials_server_id_user_id_profile_key UNIQUE (server_id, user_id, profile);

-- The callback stores the token under the profile the flow was started for.
ALTER TABLE upstream_oauth_states
    ADD COLUMN profile text NOT NULL DEFAULT 'default' CHECK (profile ~ '^[a-z][a-z0-9_-]{0,31}$');

-- Profile used by a call; NULL for calls to servers without credentials and rows before 0008.
-- Added on the partitioned parent, so every partition (present and future) gets it.
ALTER TABLE audit_events
    ADD COLUMN profile text CHECK (profile ~ '^[a-z][a-z0-9_-]{0,31}$');
