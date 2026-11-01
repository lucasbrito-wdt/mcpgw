# Contributing to mcpgw

Thanks for your interest in mcpgw, a Go MCP gateway with an embedded Next.js admin UI. This guide gets you from a fresh clone to a passing test run and a reviewable pull request.

By participating you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md). To report a security vulnerability, do not open a public issue; see [SECURITY.md](SECURITY.md).

## Prerequisites

| Tool | Version | Needed for |
|------|---------|------------|
| Go | 1.26 (`go 1.26.0` in `go.mod`) | Building and testing the gateway |
| Node.js | 22 LTS | Building and testing the admin UI |
| pnpm | 9.15.0, through corepack (`packageManager` in `web/admin/package.json`) | Admin UI dependencies and scripts |
| PostgreSQL | 18 or newer | Running the gateway and the DB tests |
| make | any | The targets listed below |
| Docker | optional | The Postgres one-liner below, stdio upstream servers, the images in `docker/`, the opt-in compat tests |
| `psql` | optional | The Playwright e2e suite only |

Notes:

- **Node and pnpm.** Run `corepack enable` once; inside `web/admin` pnpm then resolves the pinned 9.15.0. The repository does not enforce a Node version (no `engines` field, no `.nvmrc`), so use 22 LTS to match what the project is developed against.
- **PostgreSQL 18.** The first migration creates tables with the built-in `uuidv7()` default, which exists only in PostgreSQL 18 and later. The migrations use no extensions (`CREATE EXTENSION` does not appear in `migrations/`). Older versions are not supported and have not been tried.
- **Docker.** Not needed to build, run the default tests, or work on the UI, and not needed at all if you already have a PostgreSQL 18 server. The gateway can launch stdio MCP servers as hardened throwaway containers, and `docker/postgres` and `docker/redis` hold Dockerfiles for two such upstream images. You need Docker to run those servers or to change those Dockerfiles. The opt-in compat tests also use it (see [Tests](#tests)).
- **No UI build needed for Go work.** `web/admin/out/.keep` is tracked, so `go build`, `go vet` and `go test` work on a fresh clone. Until you build the UI, the gateway answers `/admin/` and `/portal/` with a "UI não compilada" placeholder page.

## Local setup

```sh
git clone https://github.com/lucasbrito-wdt/mcpgw.git
cd mcpgw
cp .env.example .env
```

Edit `.env`. The gateway reads it from the working directory at startup; variables already set in your environment win. Keep it out of version control (it is in `.gitignore`) and `chmod 600 .env`, because it holds the master key.

| Variable | Required | Meaning |
|----------|----------|---------|
| `MCPGW_DATABASE_URL` | yes | Postgres connection URL |
| `MCPGW_LISTEN_ADDR` | no | Listen address; default `127.0.0.1:8787` |
| `MCPGW_PUBLIC_URL` | no | Externally visible origin, no path; default `http://localhost:8787`. Admin and portal writes must carry this origin in the `Origin` header |
| `MCPGW_MASTER_KEY` | yes for `serve` | Base64 of 32 random bytes: `openssl rand -base64 32` |

Start a throwaway Postgres 18 with Docker (or point `.env` at an existing PostgreSQL 18 server and skip this step). The container below matches the URL in `.env.example` (the role the image creates is a superuser, which also satisfies the `CREATEDB` requirement of the tests):

```sh
docker run -d --name mcpgw-postgres -p 127.0.0.1:5432:5432 \
  -e POSTGRES_USER=mcpgw -e POSTGRES_PASSWORD=mcpgw -e POSTGRES_DB=mcp_gateway \
  postgres:18
```

Then set `MCPGW_DATABASE_URL=postgres://mcpgw:mcpgw@127.0.0.1:5432/mcp_gateway?sslmode=disable` and a freshly generated `MCPGW_MASTER_KEY` in `.env`. Use those credentials only for local development.

Build, migrate, create the first admin user and run:

```sh
make build
bin/mcpgw migrate
MCPGW_ADMIN_PASSWORD='a-password-of-12-or-more-characters' \
  bin/mcpgw admin user create --email you@example.com --admin
bin/mcpgw serve
```

- `make build` first runs `make admin` (installs the web dependencies and exports the static UI into `web/admin/out`), then compiles `bin/mcpgw`. The export is embedded in the binary with `go:embed`, so a UI change needs `make build` again.
- `migrate` applies pending migrations and prints each version. `serve` also applies pending migrations on startup, but user creation needs the schema, so run `migrate` first on an empty database.
- `admin user create` takes `--email` and optional `--admin`. The password is read from `MCPGW_ADMIN_PASSWORD` or, if that is unset, the first line of stdin, never from arguments. It must be between 12 and 1024 characters.
- Run `bin/mcpgw admin` with no arguments to list every admin command.
- With the gateway running, the admin UI is at <http://localhost:8787/admin/> and the user portal at <http://localhost:8787/portal/>.

## Web UI dev loop

```sh
cd web/admin
pnpm install
pnpm dev
```

The dev server listens on port 3001 (<http://localhost:3001/admin/> and <http://localhost:3001/portal/>) and hot-reloads components and styles.

Know what this does and does not do. `web/admin/next.config.mjs` configures a static export (`output: 'export'`) and defines no rewrites or proxy. The UI calls the API with same-origin paths (`/admin/api/...` and `/portal/api/...`), so under `pnpm dev` those requests go to the Next dev server on port 3001, not to the gateway. Use the dev server for layout, styling and components that need no data. Login and every data-backed page need the real gateway:

1. `make build`
2. Restart `bin/mcpgw serve`
3. Open <http://localhost:8787/admin/>

## Tests

### Go

```sh
make test
```

Run a single package or a single test while iterating:

```sh
go test ./internal/vault/
go test -run TestRoundTrip ./internal/vault/
go test -count=1 -run 'TestAdminEvents' ./internal/store/
```

`-run` takes a regular expression matched against test names; `-count=1` bypasses the test cache.

Tests that need a database use `internal/store/storetest`. Each one creates its own throwaway database named `mcpgw_test_<unix-time>_<random>` on the server in `MCPGW_DATABASE_URL`, applies all migrations, and drops it when the test ends. Consequences:

- The database role needs `CREATEDB`. The database named in the URL must already exist, because the helper connects to it to issue `CREATE DATABASE`.
- The tests skip when `MCPGW_DATABASE_URL` is unset. The helper looks in the environment first, then in the `.env` file at the module root. A `.env` that still holds the placeholder values from `.env.example` is not "unset": the tests will try to connect and fail.
- If a test run is killed, leftover `mcpgw_test_*` databases are dropped by the next run once they are more than two hours old.

An opt-in compatibility matrix runs real MCP servers through the gateway. It needs `node`, `npm` and network access, and one case needs a Docker daemon:

```sh
MCPGW_COMPAT=1 go test -race -count=1 -run Compat -v ./cmd/mcpgw/
```

### Admin UI unit tests

```sh
make test-admin-web
```

This runs Vitest (`pnpm test`, which is `vitest run`) over `web/admin/lib/**/*.test.ts` and `web/admin/components/**/*.test.ts` in a Node environment. To run one file:

```sh
cd web/admin
pnpm exec vitest run lib/api.test.ts
```

### End-to-end (Playwright)

```sh
cd web/admin
pnpm e2e
```

You do not start the gateway yourself. `web/admin/playwright.config.ts` registers `e2e/global-setup.ts`, which boots a complete stack and tears it down afterwards:

1. Reads `MCPGW_DATABASE_URL` from the environment or `.env` and uses only its server and credentials.
2. Runs `psql` (found on `PATH`, or set `PSQL`) to drop and recreate a database named `mcpgw_e2e` on that server. Any existing database with that name is destroyed.
3. Runs `make admin` (skip it with `E2E_SKIP_ADMIN_BUILD=1`), then builds the gateway and a fake MCP upstream (`web/admin/e2e/fakeupstream`) into a temporary directory.
4. Starts the fake upstream on `127.0.0.1:18795` and `mcpgw serve` on `127.0.0.1:18787` with a random master key. Both ports must be free.
5. Creates an admin user and a regular user with `mcpgw admin ...`, registers the fake servers through the admin CLI and API, and loads `e2e/seed.sql`.
6. After the run, stops the processes, drops `mcpgw_e2e` and deletes the temporary directory (set `E2E_KEEP_LOGS=1` to keep the logs).

Tests run with a single worker against `http://127.0.0.1:18787`. For the browser, set `E2E_CHROMIUM_PATH` to a Chromium binary, or install Playwright's own with `pnpm exec playwright install chromium`. With `E2E_CDP_URL` set, the specs attach to an already running browser over CDP instead.

## Make targets

| Target | What it does |
|--------|--------------|
| `make admin` | Installs the web dependencies (`pnpm install --frozen-lockfile`) and builds the static export into `web/admin/out` |
| `make build` | Runs `make admin`, then builds `bin/mcpgw` |
| `make test` | Runs the Go tests |
| `make test-admin-web` | Runs the admin UI unit tests (Vitest) |
| `make lint` | Checks formatting with `gofmt` and runs `go vet` |
| `make fmt` | Formats the Go code with `gofmt` |

The Playwright suite is not a make target; use `pnpm e2e` as described above.

## Conventions

- **Formatting.** Go code is `gofmt`-formatted. Run `make fmt` before committing; `make lint` fails on unformatted files.
- **Language of the code.** Identifiers, comments, test names, file names, database tables, columns and status values, and commit messages are in English (American spelling).
- **Translations (en + pt-BR).** Every user-facing string ships in both languages.
  - Web UI: add the key to a namespace in `web/admin/lib/i18n/messages/` (the type system rejects a pt-BR catalog missing an en key) and read it with `useT`; see the how-to at the top of `web/admin/lib/i18n/index.ts`.
  - Go (API errors, OAuth pages, CLI): use `i18n.T(en, pt, args...)` from `internal/i18n`; the request locale comes from `Accept-Language`, the CLI locale from `LANG`.
  - Text read by agents (code mode, tool descriptions) is English only.
- **Pull requests.** Keep them small and focused: one concern per PR. A change in behavior comes with a test that fails without it. Do not mix refactoring of unrelated code into a feature or a fix.
- **Migrations.** Schema changes are append-only, numbered SQL files named `migrations/NNNN_name.sql`, using the next number after the highest one present. They run in filename order, one transaction per file, and each applied version is recorded in `schema_migrations`. A shipped migration is never edited: databases that already recorded it will not run it again, so the edit would only reach new installs and the schemas would diverge. Fix forward with a new file.
- **Commit messages.** [Conventional Commits](https://www.conventionalcommits.org/): `feat:`, `fix:`, `chore:`, `docs:`, `test(scope):`, `refactor:`, with an optional scope, for example `feat(oauth): ...` or `test(storetest): ...`. Write the subject in the imperative, in English.
- **License.** The project is licensed under Apache-2.0 (see [LICENSE](LICENSE)). Under section 5 of that license, any contribution you intentionally submit is licensed under the same terms, with no additional conditions. There is no CLA to sign.

## Submitting a pull request

1. For anything larger than a small fix, open an issue first so the approach can be agreed before you invest time.
2. Fork the repository and create a branch from `main`.
3. Make the change, with tests.
4. Run `make fmt`, `make lint` and `make test`. If you touched `web/admin`, also run `make test-admin-web`.
5. Open the pull request and fill in the template: summary, linked issue, type of change and the checklist. The checklist asks you to confirm that:
   - tests are added or updated;
   - `make lint` and `make test` pass;
   - a schema change is a new migration, not an edit to an existing one;
   - new user-facing copy exists in en and pt-BR;
   - documentation is updated.
6. Respond to review comments by pushing further commits to the same branch.

## Reporting bugs and requesting features

Use the issue forms under **Issues > New issue**. For a bug report, include:

- **The version and commit.** In the admin UI, open Configurações (Settings): it lists "Versão" and "Commit". A binary you built yourself reports a version derived from the commit (a trailing `+dirty` means uncommitted changes), or `(devel)` when it was built outside a git checkout, so always include the commit. `go version -m bin/mcpgw` prints it as `vcs.revision`, and `git rev-parse HEAD` gives it from your checkout.
- Your OS, your Postgres version and which MCP client you use (Claude Code, Codex, Cursor or other).
- What happened, what you expected, and the smallest set of steps that reproduces it.
- Relevant logs, with secrets removed. Redact tokens, the master key, database URLs and cookies before pasting.

Do not report security problems in a public issue. Follow [SECURITY.md](SECURITY.md) instead.
