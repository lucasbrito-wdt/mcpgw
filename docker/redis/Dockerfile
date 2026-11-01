# mcp/redis with an optional per-call `db` argument; runs the venv directly (the base `uv run` fails on --read-only).
FROM mcp/redis@sha256:e886a7e9990a084a20d46adcea8e7c89d539271f1b453ffabdce4c3f96f27fa0
COPY db_select.py /mcpgw/db_select.py
WORKDIR /app
CMD ["/app/.venv/bin/python", "/mcpgw/db_select.py"]
