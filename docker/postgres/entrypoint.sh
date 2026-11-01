#!/bin/sh
# Lets each mcpgw connection pick its access mode and connect from libpq PG* variables
# (PGHOST, PGPORT, PGUSER, PGPASSWORD, PGDATABASE, PGSSLMODE...) instead of one DATABASE_URI.
mode="${POSTGRES_ACCESS_MODE:-restricted}"
case "$mode" in
  restricted | unrestricted) ;;
  *)
    echo "POSTGRES_ACCESS_MODE must be restricted or unrestricted, got: $mode" >&2
    exit 64
    ;;
esac
export DATABASE_URI="${DATABASE_URI:-postgresql://}"
exec /app/docker-entrypoint.sh postgres-mcp --access-mode="$mode" "$@"
