<p align="center">
  <img src="web/admin/brand/icon.svg" width="96" height="96" alt="mcpgw logo">
</p>

<h1 align="center">mcpgw</h1>

<p align="center"><strong>One MCP address for your agents.</strong></p>

<p align="center">
  <a href="https://github.com/lucasbrito-wdt/mcpgw/actions/workflows/ci.yml"><img src="https://github.com/lucasbrito-wdt/mcpgw/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="License: Apache-2.0"></a>
</p>

<p align="center">English | <a href="README.pt-BR.md">Português (Brasil)</a></p>

mcpgw is a self-hosted gateway for the [Model Context Protocol](https://modelcontextprotocol.io),
written in Go. Your agents connect to a single URL. Behind it you attach any number of MCP servers,
decide who may call what, and keep every credential on the server side. It ships as one binary and
needs only PostgreSQL.

Instead of loading every tool schema into the agent's context, mcpgw exposes two tools, `search`
and `execute`. The agent searches for what it needs, then writes a short JavaScript function that
calls `tools.<server>.<tool>()` inside a sandbox.

## Why mcpgw

- **Small context, many servers.** The agent sees two tools, not hundreds of schemas, and chains
  calls in code instead of round trips through the model.
  See [code mode](docs/ARCHITECTURE.md#code-mode-instead-of-tool-schemas) and
  [the sandbox](docs/ARCHITECTURE.md#the-javascript-sandbox).
- **The right key for every call.** A user's personal key is used when they have one, otherwise the
  team's shared key, and one person can keep several named accounts per server.
  See [credential resolution](docs/ARCHITECTURE.md#credential-resolution) and
  [connection profiles](docs/ARCHITECTURE.md#connection-profiles).
- **Least privilege per user and per agent.** An admin ceiling and the user's own consent must both
  allow a tool. See [policy ceilings and consent](docs/ARCHITECTURE.md#policy-ceilings-and-consent).
- **Tool definitions cannot change under you.** Every tool is pinned to a hash, and a changed
  definition is held back until an admin reviews it.
  See [catalog trust](docs/ARCHITECTURE.md#catalog-trust).
- **Secrets stay secret, and everything is on record.** Credentials are sealed in Postgres and never
  enter the sandbox, and each execution and inner call is audited.
  See [credentials at rest](docs/ARCHITECTURE.md#credentials-at-rest) and
  [audit](docs/ARCHITECTURE.md#audit).
- **One binary, one database.** The admin and portal UI is embedded, and changes made anywhere reach
  every running gateway without a restart. See
  [the embedded UI](docs/ARCHITECTURE.md#embedded-ui-and-its-csp) and
  [hot reload](docs/ARCHITECTURE.md#hot-reload-with-listen-and-notify).

## Features

- One MCP endpoint, `/mcp`: Streamable HTTP, stateless, MCP protocol version `2026-07-28`.
- Code mode: `search` returns TypeScript declarations for the tools a user may call, `execute` runs
  the agent's JavaScript in QuickJS compiled to WebAssembly (no cgo).
- Upstream transports: Streamable HTTP, legacy SSE and stdio. Stdio servers run in a hardened
  throwaway Docker container by default.
- Upstream authentication: none, OAuth (discovery, PKCE, resource indicators, client metadata
  documents or dynamic registration), a pasted header token, or a per-user environment variable for
  stdio servers.
- Personal and shared connections, and named connection profiles chosen per call.
- OAuth 2.1 authorization server for MCP clients, with client metadata documents and dynamic client
  registration, PKCE, ES256 access tokens and rotating refresh tokens.
- Trust levels (`read`, `write`, `destructive`), per-user ceilings, per-agent consent, and tool
  pinning with review.
- Audit trail of executions and inner tool calls, plus an admin action log.
- Credentials encrypted at rest with AES-256-GCM.
- Hot reload through Postgres `LISTEN`/`NOTIFY`.
- Web admin and user portal, and an admin CLI.
- `GET /healthz`, and optional pprof on a loopback address.

mcpgw proxies tools. Upstream resources and prompts are not exposed.

## Quickstart

### Requirements

- Go 1.26 or newer (see `go.mod`).
- PostgreSQL 18 or newer. The migrations use the built-in `uuidv7()`.
- Node.js 22 and pnpm 9, only to build the web UI. Without them, `go build ./cmd/mcpgw` still
  works, but `/admin/` and `/portal/` answer 503 with a placeholder page.
- Docker, only if you want stdio MCP servers in containers.

### 1. Configure

```bash
git clone https://github.com/lucasbrito-wdt/mcpgw.git
cd mcpgw
cp .env.example .env
chmod 600 .env
```

Create an empty database (for example `createdb mcp_gateway`), then edit `.env`. Set
`MCPGW_DATABASE_URL`, and generate the master key that seals every secret:

```bash
openssl rand -base64 32   # paste the output as MCPGW_MASTER_KEY
```

The binary reads `.env` from the current directory. Variables already set in the environment win.
Back the master key up: sealed values cannot be opened without it.

### 2. Build and migrate

```bash
make build                # builds the web UI, then bin/mcpgw
bin/mcpgw migrate         # serve also applies pending migrations at startup
```

### 3. Create the first admin

The password takes 12 to 1024 characters. It is read from `MCPGW_ADMIN_PASSWORD` or from the first
line of stdin, never from argv.

```bash
export MCPGW_ADMIN_PASSWORD='a-long-passphrase-of-your-choice'
bin/mcpgw admin user create --email admin@example.com --admin
```

### 4. Serve

```bash
bin/mcpgw serve
curl http://127.0.0.1:8787/healthz   # prints "ok" when the database is reachable
```

### 5. Give agents something to call

A new gateway has no servers, and admins get no implicit access. Add an upstream server, sync its
catalog, and grant yourself a ceiling on it. The first sync of a server sets the trusted baseline
for its tools.

```bash
bin/mcpgw admin server add --slug acme --url https://mcp.example.com/mcp --auth none
bin/mcpgw admin catalog sync --slug acme
bin/mcpgw admin ceiling set --email admin@example.com --slug acme --level write
```

You can do the same in the web admin. Other kinds of server:

```bash
# OAuth upstream: users connect their account in the portal, then an admin syncs as that user
bin/mcpgw admin server add --slug globex --url https://mcp.example.com/globex --auth oauth
bin/mcpgw admin catalog sync --slug globex --as admin@example.com

# stdio server in a hardened container, without network access
bin/mcpgw admin server add --slug files --transport stdio \
  --docker-image ghcr.io/acme/files-mcp:1.0 --auth none
```

### 6. Connect your agents

The gateway URL is your public URL plus `/mcp`: `http://localhost:8787/mcp` for a local run.

```bash
# Claude Code
claude mcp add --transport http mcpgw http://localhost:8787/mcp

# Codex
codex mcp add mcpgw --url http://localhost:8787/mcp
codex mcp login mcpgw
```

In Claude Code, authenticate from the `/mcp` menu. For Cursor, open Settings, MCP, add a new server
with the URL, or put `{"mcpServers": {"mcpgw": {"url": "http://localhost:8787/mcp"}}}` in
`~/.cursor/mcp.json`. The portal also shows these instructions at `/portal/agents/`.

On the first connection the client opens the gateway's login page. After signing in, you choose an
access level for each server, up to your ceiling. The agent then sees `search` and `execute`.

**Protocol version.** The gateway accepts only MCP protocol version `2026-07-28` and answers any
other version with HTTP 400 that names the supported one. A client that cannot speak it fails at the
first request. These client commands come from each client's own CLI and from the portal, and have
not been tested against specific client releases.

### Production notes

- The gateway serves plain HTTP. Put a TLS-terminating reverse proxy in front, and set
  `MCPGW_PUBLIC_URL` to the external `https` origin. It is the OAuth issuer, and https turns on
  `Secure` cookies and HSTS.
- Route every path in the table below to the gateway, not only `/mcp`.
- `X-Forwarded-For` is not trusted, so per-IP rate limits see the proxy's address.
- Run the gateway as an unprivileged user, and keep `.env` at mode `0600`.

## Configuration

All settings are environment variables. `.env.example` lists the first four.

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `MCPGW_DATABASE_URL` | yes | none | PostgreSQL URL. The pool defaults to 16 connections, and `pool_max_conns` in the URL overrides it. |
| `MCPGW_MASTER_KEY` | for `serve` | none | Base64 of 32 random bytes. Seals all stored secrets. Admin commands that seal or open secrets need it too. |
| `MCPGW_LISTEN_ADDR` | no | `127.0.0.1:8787` | `host:port` the gateway binds to. |
| `MCPGW_PUBLIC_URL` | no | `http://localhost:8787` | External origin, with no path. It is the OAuth issuer and the base of every link the gateway builds. |
| `MCPGW_PPROF_ADDR` | no | unset | Loopback `host:port` that serves `net/http/pprof`. Non-loopback values are rejected. |
| `MCPGW_ADMIN_PASSWORD` | no | none | Read only by `mcpgw admin user create`. Falls back to the first line of stdin. |
| `NO_COLOR` | no | unset | Any value turns off colors in the startup banner. |

What the gateway serves on its single port:

| Path | Purpose |
| --- | --- |
| `/mcp` | The MCP endpoint for agents. |
| `/authorize`, `/token`, `/register`, `/jwks.json` | OAuth authorization server for MCP clients. |
| `/.well-known/oauth-authorization-server`, `/.well-known/oauth-protected-resource` and `/.well-known/oauth-protected-resource/mcp` | OAuth discovery documents. |
| `/connect/{slug}`, `/connect/callback`, `/oauth/client-metadata.json` | The gateway connecting a user's account to an upstream server. |
| `/admin/`, `/admin/api/` | Admin web UI and its API. |
| `/_next/` and a few root brand files such as `/favicon.ico` | Static assets of the web UI. |
| `/portal/`, `/portal/api/` | User portal and its API. |
| `/healthz` | Health check. |

## Sandbox engines

Code mode runs scripts in a sandbox. Two engines exist:

- `MCPGW_SANDBOX_ENGINE`: `qjs` (default, QuickJS in WebAssembly) or `monty` (Python in a native Rust worker).
- `MCPGW_MONTYRS_WORKER`: path to the worker used by `monty`. Default: `montyrs-worker` next to the `mcpgw` executable, resolved to an absolute path.
- `make worker` builds the worker into `bin/montyrs-worker` (`.exe` on Windows). `make build` runs it too.
- On Windows the worker runs inside an AppContainer, which needs read and execute on the exe: `make worker` grants it; for a worker copied elsewhere run `icacls montyrs-worker.exe /grant "*S-1-15-2-1:(RX)"`.

Security per platform: the worker confines itself at the OS level.

- Linux (strongest): syscall allowlist (seccomp), `no_new_privs` and rlimits.
- Windows: AppContainer, Job Object and process mitigation policies.
- macOS: Seatbelt denies network and file access, but it does not filter syscalls or prevent executable memory (W^X is only the OS default). A full escape needs breaking the Monty interpreter and then Seatbelt.

Prefer Linux for multi-tenant production.

## Web admin and portal

The UI is a Next.js static export embedded in the binary (`web/admin`), served from the same origin
as the API.

- `/admin/` is for admin users. It manages servers (add, test, sync, header tokens, OAuth clients),
  tool review, per-user ceilings, users, OAuth clients and consents, shared connections,
  execution logs and the admin action log. Writes that store secrets ask for your password again.
- `/portal/` is for every user. It connects your own accounts to servers by OAuth or by pasting a
  token, supports named profiles, and shows how to install the gateway in each agent.

**Language.** The web UI, the login and consent pages and the API error messages are available in
English and Brazilian Portuguese. The browser language picks the default (`Accept-Language`), and a
switcher in the sidebar and on the login pages overrides it. The CLI follows `LANG`/`LC_ALL`.
Messages read by agents (code mode errors, tool descriptions) are English only.

## CLI overview

```text
mcpgw serve      run the gateway
mcpgw migrate    apply pending database migrations
mcpgw admin      administration commands
```

Run `bin/mcpgw admin` to print the full usage.

| Group | Commands |
| --- | --- |
| `user` | `create`, `list`, `set-admin` |
| `server` | `add`, `list`, `enable`, `disable`, `remove`, `set-header`, `set-header-format`, `set-oauth-client`, `set-trust`, `set-env`, `set-credential-env`, `set-connection-env`, `set-connection-url` |
| `ceiling` | `set`, `list` |
| `tool` | `list`, `show`, `level`, `approve` |
| `catalog` | `sync` |

Secrets never travel through argv. Flags such as `--value-env`, `--secret-env` and `--env NAME=VAR`
name an environment variable that holds the value.

## Development

```bash
make build            # web UI, then bin/mcpgw
make admin            # rebuild only the embedded web UI
make test             # Go tests
make test-admin-web   # web UI unit tests
make lint             # gofmt check and go vet
make fmt              # gofmt
```

Tests that need Postgres skip themselves unless `MCPGW_DATABASE_URL` is set. They create and drop
throwaway databases, so use a role that can `CREATE DATABASE`.

For how the pieces fit together, read [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md). Design specs
live in [docs/specs/](docs/specs/).

## Community

- [CONTRIBUTING.md](CONTRIBUTING.md)
- [SECURITY.md](SECURITY.md)
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- [README.pt-BR.md](README.pt-BR.md), the Brazilian Portuguese version of this file

## License

Licensed under the [Apache License 2.0](LICENSE). [NOTICE](NOTICE) covers `third_party/qjs`, a
patched copy of [fastschema/qjs](https://github.com/fastschema/qjs) and the QuickJS-ng WebAssembly
build it ships, both under the MIT license.
