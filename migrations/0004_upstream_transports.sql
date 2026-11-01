-- Universal upstreams (W5): transport selection, stdio process specs and remembered detection.

ALTER TABLE upstream_servers
    -- auto: Streamable HTTP first, legacy SSE fallback. stdio rows use url 'stdio://<id>'.
    ADD COLUMN transport text NOT NULL DEFAULT 'auto'
        CHECK (transport IN ('auto', 'streamable_http', 'sse', 'stdio')),
    -- docker (hardened container, the default) or host (explicit admin opt-in).
    ADD COLUMN stdio_launcher text CHECK (stdio_launcher IN ('docker', 'host')),
    -- host: absolute executable path. docker: optional command after the image.
    ADD COLUMN stdio_command text,
    ADD COLUMN stdio_args text[] NOT NULL DEFAULT '{}',
    ADD COLUMN docker_image text,
    -- Docker containers run with --network none unless this is set.
    ADD COLUMN stdio_network boolean NOT NULL DEFAULT false,
    -- JSON object of the complete server environment, sealed with the vault,
    -- AAD "upstream_servers:<id>:stdio_env". Nothing is inherited from the gateway.
    ADD COLUMN stdio_env_ciphertext bytea,
    -- Last transport and protocol version that worked, so new sessions skip probing.
    ADD COLUMN detected_transport text CHECK (detected_transport IN ('streamable_http', 'sse', 'stdio')),
    ADD COLUMN detected_protocol_version text CHECK (detected_protocol_version ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$'),
    ADD COLUMN detected_at timestamptz,
    ADD CONSTRAINT upstream_servers_stdio_url
        CHECK ((transport = 'stdio') = (url LIKE 'stdio://%')),
    ADD CONSTRAINT upstream_servers_stdio_spec CHECK (
        transport <> 'stdio'
        OR (stdio_launcher = 'docker' AND docker_image IS NOT NULL)
        OR (stdio_launcher = 'host' AND stdio_command IS NOT NULL AND docker_image IS NULL AND NOT stdio_network)),
    ADD CONSTRAINT upstream_servers_stdio_fields CHECK (
        transport = 'stdio'
        OR (stdio_launcher IS NULL AND stdio_command IS NULL AND docker_image IS NULL
            AND stdio_env_ciphertext IS NULL AND stdio_args = '{}' AND NOT stdio_network)),
    -- stdio servers take secrets through their sealed environment, never HTTP auth.
    ADD CONSTRAINT upstream_servers_stdio_auth CHECK (transport <> 'stdio' OR auth_kind = 'none');

CREATE INDEX upstream_servers_url_idx ON upstream_servers (url);
