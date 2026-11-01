# crystaldba/postgres-mcp with per-connection access mode (POSTGRES_ACCESS_MODE) and libpq PG* variables.
FROM crystaldba/postgres-mcp:0.3.0
COPY --chmod=755 entrypoint.sh /mcpgw/entrypoint.sh
ENTRYPOINT ["/mcpgw/entrypoint.sh"]
