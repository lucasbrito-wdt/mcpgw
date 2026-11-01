---
title: Admin web UI (phase 2)
date: 2026-10-29
status: draft
decisions:
  ui_stack: Next.js static export embedded in the Go binary (go:embed), same origin
  admin_auth: login with users.is_admin, sealed __Host- cookie, CSRF, re-auth for secrets
  user_portal: every user manages their own upstream connections at /portal (same Next app)
  credentials: personal connection overrides the shared one (Composio "For you" / "For your team"); server declares only what the upstream accepts (none | oauth | header)
  surfaces: portal and admin both dark (Airbyte Cloud style, violet #5F5CFF); the portal gets a stronger nebula backdrop
  mockups: design/v5 (Next app) approved 2026-10-31; design/admin (v2), v3 and v4 superseded
---

# Admin web UI

## Context

Phase 1 ships the admin surface only as the `mcpgw admin` CLI (`cmd/mcpgw/admin*.go`, ~1.1k
lines). Every operation mixes flag parsing, SQL and table printing in package `main`, so an HTTP
API cannot reuse it as is. `users.is_admin` exists in the schema (0001) but no Go code reads it.
The OAuth server already has the pieces an admin login needs: argon2id `authenticate` with
bounded password slots, a login rate limiter, a sealer for stateless cookies and a strict CSP.

## Decision

- **Stack:** Next.js 15 App Router with `output: 'export'`, built to `web/admin/out` and embedded
  with `go:embed`. One binary, one origin (`/admin` static, `/admin/api` JSON), no Node at runtime.
  Rejected: Go templates + htmx (smallest, but weak for the tool review and audit filters);
  Next.js server as a second process (two deploys, CORS or proxy, Node in production).
- **Auth:** admins are `users.is_admin = true`. Separate login at `/admin/login`, stateless sealed
  session cookie, CSRF on every write, password re-entry for writes that store secrets.
  Rejected: static bearer token (no actor attribution, leak risk).
- **Shared core:** extract the CLI logic into `internal/admin` (typed inputs, typed results,
  validation, SQL). The CLI becomes flag parsing + printing over it; the HTTP API becomes JSON over
  it. No operation is implemented twice.
- **Scope boundary (security):** the web UI cannot create or edit `stdio` servers with
  `launcher=host` (arbitrary executable on the gateway host). Those stay CLI-only. Docker stdio
  and HTTP servers are allowed.
- **User portal:** any gateway user (not only admins) gets `/portal` to connect their own
  credentials to each upstream server, sized for 40-80 servers (search, status filters, "Precisa
  de ação" queue, bulk disconnect). Login reuses the OAuth server session (`mcpgw_session`, the
  same one `/connect/{slug}` already requires), so there is one user login, not two.
- **Credentials: personal over shared** (user decision 2026-10-30, modeled on Composio's
  "Connect account: For you / For your team"). **SUPERSEDES the earlier "credential mode per
  server / user_header" decision; wherever this spec still says `user_header` read it as
  `auth_kind = header`.**
  - The server declares only what the upstream accepts: `auth_kind` `none`, `oauth` or `header`
    (`header` = today's `static_header`, renamed; config: header name, value prefix e.g.
    `Bearer `, format hint e.g. `github_pat_`, help URL).
  - A connection is either **personal** (one per server + user, only that user's calls use it) or
    **shared** (at most one per server, used by every user who has no personal connection).
  - Resolution on each call: personal connection of the caller -> shared connection -> not
    connected (`ErrNotConnected` with the portal URL). `none` needs nothing.
  - Who creates what: any user creates/replaces/removes their own personal connection (OAuth or
    token, by `auth_kind`); only admins create/replace/remove the shared one (from the admin UI or
    the portal "Para todos" option, which is hidden for non-admins). Rationale: a shared
    connection exposes one account to every user, so it is an admin act and is audited.
  - Storage: both live in `upstream_credentials` (sealed, write-only, shown as `••••` + last 4)
    with `shared boolean`; personal rows keep `UNIQUE (server_id, user_id) WHERE NOT shared`,
    shared rows `UNIQUE (server_id) WHERE shared`; a shared row has `user_id` NULL and
    `connected_by` = the admin who connected it (attribution). The existing server-level `static_header_ciphertext` is migrated
    into a shared row by a Go step (it must be re-sealed with the credential AAD), then the
    columns are dropped in a later contract migration.
  - Portal shows per server which connection is in effect: "Sua conexão", "Conexão
    compartilhada (fornecida pelo admin)" or "Não conectada".
  Rejected: fixed mode per server (forces every server to all-personal or all-shared) and
  letting any user share their own account with everyone.

## Spec-delta

### ADDED

- `internal/admin`: service over `pgxpool.Pool` + `upstreamauth.Manager` + `catalog.Lister`.
  Operations: users (list, create, set admin, reset password), servers (list, get, add http/docker
  stdio, enable, disable, set header, set oauth client, set trust), tools (list, show, set level,
  approve one by hash, approve all for a server), ceilings (list, set, clear), catalog sync,
  clients (list), consents (list, revoke), grants (list families, revoke family), executions
  (list, get with audit events), admin log (list).
- `internal/adminapi`: HTTP handlers under `/admin/api`, session, CSRF, admin audit writer.
- `web/admin`: Next.js app (static export) + `web/admin/embed.go` (`package adminweb`, exposes
  `fs.FS` of `out/`).
- Migration `0006_admin.sql`: `admin_events` table (who did what), see Contracts.
- Migration `0007_user_credentials.sql`:
  - `upstream_servers.auth_kind` CHECK gains `user_header`; new nullable columns
    `user_header_name`, `user_header_prefix` (default `''`), `user_header_format_hint`,
    `user_header_help_url`; CHECK: `auth_kind = 'user_header'` requires `user_header_name`.
  - `upstream_credentials`: `account_label text` (login/workspace shown to the user, from the
    test call or the OAuth userinfo when available), `last4 text` (user_header only),
    `last_used_at timestamptz`, `status text` (`active` / `refresh_failed` / `revoked_by_admin`),
    `revoked_reason text`.
- `upstreamauth`: `CredentialFor` handles `user_header` (open the user's sealed token, build
  `name: prefix+token`, `ErrNotConnected` with the portal URL when missing); `SetUserToken`,
  `TestUserToken` (calls the upstream `initialize` + `tools/list` with the candidate token, never
  stores it on failure); `Disconnect`; refresh failure marks `status = refresh_failed`.
- `internal/portalapi`: HTTP handlers under `/portal/api` for the signed-in user's connections.
- `web/admin` app gains the `/portal/*` routes (same static export, same embed).
- CLI: `user create --admin`, `user set-admin --email E --admin=true|false` (bootstrap of the
  first admin is CLI-only by design).

### MODIFIED

- `cmd/mcpgw/admin*.go`: call `internal/admin`; output text unchanged (existing admin tests are
  the regression net).
- `cmd/mcpgw/gateway.go`: mount `/admin` (static) and `/admin/api` on the same mux.
- `oauth.Server`: export what admin login needs (`Authenticate`, login limiter, sealer) without
  duplicating it, and `RevokeFamily` so revocation also clears the in-process grant cache.

### REMOVED

- Nothing.

## Architecture

```
browser ── GET /admin/*      -> embedded static files (adminweb.FS), CSP with script hashes
        └─ /admin/api/*      -> adminapi: requireAdmin -> csrf (writes) -> handler
                                   -> internal/admin (validation + SQL + vault)
                                   -> admin_events (one row per write, in the same tx)
cmd/mcpgw admin ...          -> internal/admin (same functions)
```

Static export constraints accepted:
- No dynamic segments; detail pages use query strings (`/admin/servers/view?slug=github`).
- No Next middleware; the client calls `GET /admin/api/me` and redirects to `/admin/login` on 401.
  Real enforcement is server side only.
- Next inlines bootstrap scripts. At startup the Go side walks the embedded HTML, computes
  sha256 of every inline `<script>` and serves `script-src 'self' 'sha256-...'`. No
  `unsafe-inline`, no `unsafe-eval`.
- `go build` needs `web/admin/out`. A `make admin` target builds it; a tiny placeholder
  `out/index.html` is committed so `go build`/`go test` work without Node.

## Contracts

All responses JSON. Errors: `{"error": {"code": "...", "message": "..."}}` with codes
`unauthorized` 401, `forbidden` 403, `csrf` 403, `reauth_required` 403, `not_found` 404,
`invalid` 422 (`fields` map), `conflict` 409, `rate_limited` 429. Lists: `?limit` (default 50,
max 200) + `?cursor` (opaque, uuidv7 keyset), response `{"items": [...], "next_cursor": "..."}`.

Session and CSRF:
- `POST /admin/api/session {email, password}` -> 204, sets `__Host-mcpgw_admin` (sealed:
  uid, iat, last_seen, reauth_at) and `__Host-mcpgw_admin_csrf` (readable). Non-admin or wrong
  password -> the same 401. Shares the OAuth login rate limiter and password slots.
- Cookie: HttpOnly, Secure, SameSite=Strict, Path=/ (the `__Host-` prefix forbids any other
  path; scoping is done server side, only `/admin/api` reads it). Idle 30 min (sliding, re-sealed on use),
  absolute 8 h. On plain http (dev) the `__Host-` prefix is dropped as the OAuth cookies do.
- `DELETE /admin/api/session` -> 204, clears both cookies.
- `POST /admin/api/session/reauth {password}` -> 204, sets `reauth_at`. Writes that store
  secrets (`set header`, `set oauth client` with secret, `reset password`) require
  `reauth_at` within 5 min, else 403 `reauth_required`.
- Every non-GET requires header `X-CSRF-Token` equal to the csrf cookie (double submit, tied to
  the session uid by sealing) and `Origin` equal to the gateway origin.
- Every request re-checks `is_admin` in the database (demoting an admin takes effect at once).

Resources:

| Method | Path | Body / query | Notes |
|---|---|---|---|
| GET | /me | | `{email}` |
| GET | /overview | | counts: servers, tools by status, pending approvals, denies 24h, executions 24h |
| GET | /users | | without password_hash |
| POST | /users | `{email, password, is_admin}` | password min 12 chars |
| PATCH | /users/{id} | `{is_admin}` | refuses demoting the last admin (409) |
| POST | /users/{id}/password | `{password}` | reauth |
| GET | /servers | | includes `has_static_header`, `has_oauth_client`, sync state |
| GET | /servers/{slug} | | never returns header values or secrets |
| POST | /servers | `{slug, url, auth_kind, transport}` or docker stdio fields | `launcher=host` -> 422 |
| PATCH | /servers/{slug} | `{status?, trust_annotations?, auto_approve_new_tools?}` | |
| PUT | /servers/{slug}/header | `{name, value}` | reauth; write-only |
| PUT | /servers/{slug}/oauth-client | `{client_id, client_secret?}` | reauth if secret |
| POST | /servers/{slug}/sync | | runs catalog sync, returns SyncReport |
| GET | /tools | `?server=&status=` | |
| GET | /tools/{slug}/{tool} | | current definition, hash, approved_hash, pending_changes |
| PATCH | /tools/{slug}/{tool} | `{level}` | |
| POST | /tools/{slug}/{tool}/approve | `{hash}` | full or 12+ hex prefix; 409 if definition changed |
| POST | /servers/{slug}/approve-all | | |
| GET | /ceilings | `?user=` | |
| PUT | /ceilings/{user_id}/{slug} | `{level}` | `none` deletes |
| GET | /clients | | oauth_clients, metadata summary |
| GET | /consents | `?user=&client=` | |
| DELETE | /consents/{id} | | sets revoked_at |
| GET | /grants | `?user=` | active refresh families |
| DELETE | /grants/{family_id} | | `oauth.Server.RevokeFamily` |
| GET | /executions | `?user=&status=&from=&to=` | code truncated to 2 KB in list |
| GET | /executions/{id} | | full code + its audit_events |
| GET | /admin-events | `?actor=` | |

| POST | /servers | ... `auth_kind=user_header` + `{user_header_name, user_header_prefix?, user_header_format_hint?, user_header_help_url?}` | help URL must be https |
| GET | /connections | `?server=&status=` | per server: counts connected / expiring / expired / refresh_failed / not connected; per user rows with status, account_label, last_used_at. Never the token. |
| DELETE | /connections/{server_slug}/{user_id} | `{reason}` | sets `revoked_by_admin` + reason, deletes ciphertexts, audited |

`admin_events (id uuidv7, ts, actor_user_id, action text, target text, details jsonb, ip text)`;
details never contain secret values (header value, client secret, passwords).

User portal (`/portal/api`, any signed-in user, session = existing `mcpgw_session`, same CSRF
double submit + Origin rule for writes, every query scoped to the session user):

| Method | Path | Body / query | Notes |
|---|---|---|---|
| GET | /me | | `{email, is_admin}` |
| GET | /connections | `?q=&status=&auth=&category=&sort=&cursor=` | servers the user has a ceiling > none on, with mode, status (`connected`, `expiring` (< 7 d), `expired`, `refresh_failed`, `revoked_by_admin`, `scopes_changed`, `not_connected`, `ready` for none/static), account_label, last_used_at, tool count; plus `summary` counts |
| GET | /connections/{slug} | | detail: mode, scopes granted vs required, last4, expires_at, timeline, tools with level |
| POST | /connections/{slug}/oauth | | returns `{redirect_url}` of the existing `/connect/{slug}` flow; success returns to `/portal/connections?connected={slug}` |
| POST | /connections/{slug}/token/test | `{token}` | 200 `{account_label?, tools}` or 422 `upstream_rejected` with upstream status; nothing stored |
| PUT | /connections/{slug}/token | `{token}` | stores sealed, sets last4; 422 if format hint prefix does not match |
| DELETE | /connections/{slug} | | disconnect own credential |
| POST | /connections/disconnect | `{slugs: [...]}` | bulk, max 100 |

The portal never lists servers the user has no ceiling on, and a user can never read or write
another user's credential (404, not 403, to avoid enumeration).

## Design system

Source of truth: `design/v5/` (approved 2026-10-31), a Next app whose components are ported into
`web/admin`. It replaces the earlier reuse of an external `redesign-v2` design system: the gold brand,
the light portal and the plain-CSS vendoring are dropped.
- Stack: Next 16 App Router (`output: 'export'`), React 19.2, Tailwind CSS v4 CSS-first (`@theme`
  in `globals.css`, no `tailwind.config`), shadcn components over Base UI, `motion` (`motion/react`),
  `lucide-react`, `simple-icons` for server logos. Tokens and components are copied from
  `design/v5/src` (header comment with origin and date), not re-invented.
- Tokens (`design/v5/src/app/globals.css`): surfaces `void #07061A`, `ink #0C0C0E`, `panel #18181B`,
  `tile #1B1838`, `haze #3B2A7E`; accent `signal #5F5CFF`, `signal-fg #A5A3FF` (text and icons on dark,
  AA contrast), `core #8B7BFF`, `spark #ECEAFF`, `signal-wash #2B2749`, `guide #272239`; text `fg #F8F9FC`,
  `fg-muted #AEAEC1`, `fg-subtle #707089` (borders only); lines `#FFFFFF14` / `#FFFFFF26`; states
  `ok #3DDC97`, `warn #F5B84B`, `cut #FF5C7A`; radii control 6, card 10, tile 14, panel 20; one easing
  `ease-switch cubic-bezier(.22,1,.36,1)`.
- Fonts: Inter (UI) and Geist Mono (hashes, code, ids), self-hosted with `next/font` so the CSP
  stays `font-src 'self'` (no Google Fonts at runtime).
- Signature ("Switchboard"): agents -> mcpgw core -> key node -> server grid with real logos, dotted
  wires with light packets (`components/signature/*`); `RouteLine` mini in logs/cards and hero on the
  server detail; WebGL `NebulaField` backdrop (stronger on the portal). Every animation honours
  `prefers-reduced-motion`; WebGL failure falls back to the static gradient.
- Components (`components/ui/*`): button, badge/pill, dialog/drawer, popover, toggle, search field
  ("/" shortcut), chip ruler, segmented tabs, empty state, mono label, copy button; shell
  (`AppShell`, `PageHeader`); brand (`ServiceLogo`, monogram fallback).
- CSP compatibility (plan gap G26): no `style` attributes in server-rendered HTML and no `unsafe-*`
  in the CSP. Dynamic values (sizes, colours, filters, motion initial state) are applied after mount
  through refs / CSS variables or Tailwind classes; inline `<style>` blocks, if any, are hashed like
  scripts.
- Brand: product name "mcpgw" with its mark; no third-party logo.

## Screens (PT-BR copy)

Approved mockups in `design/v5/` (`npm run dev` there, open `/`, which lists every route and state);
they are the visual contract:
- Portal: `/portal/` (catalog grid + category ruler, #shared), `/portal/servers/[slug]/` (detail with
  route hero, tools, connect by token or OAuth, "Só para mim / Para todos", disconnect; states #token,
  #oauth, #oauth-denied, #personal, #disconnect), `/portal/agents/` ("Conectar agentes").
- Admin: `/admin/servers/` (list + add wizard Nenhum / OAuth / Token with header preview and the
  `prefix_already_in_value` error), `/admin/connections/` (shared connections, revoke with reason,
  503 error, bulk, empty), `/admin/logs/` (live every 1.5 s, drawer, denied/empty/error, Code Mode
  runs), `/admin/review/` (tool diff, prompt-injection flag, approve, empty).
- Hash states in the mockups become React state in the app; mock data in `design/v5/src/data` is
  replaced by the API.
- Not mocked, build from the same components: admin overview, server detail, access (ceiling
  matrix), clients, users, admin log, settings, login screens.

## Errors and edge cases

- Tool definition changes between review and approve -> 409, UI reloads the tool.
- Server slug reserved (`callback`, `success`) or invalid -> 422 with field error.
- Header/secret values are never echoed back; the UI shows "configurado em <data>".
- Executions code and tool output are untrusted: rendered as text only, never HTML/markdown.
- Catalog sync on a server with no ceiling for the admin -> uses DBResolver as the CLI does.
- Embedded build missing (placeholder) -> `/admin` shows "UI não compilada, rode make admin".

## Tests (acceptance criteria -> test names)

Go (`internal/adminapi`, httptest + real Postgres like the existing suites):
- GIVEN a non-admin user WHEN POST /session THEN 401 (TestSessionRejectsNonAdmin)
- GIVEN an admin session WHEN the user is demoted THEN the next request is 403 (TestDemotedAdminLosesAccess)
- GIVEN a write without X-CSRF-Token or with foreign Origin THEN 403 csrf (TestWriteRequiresCSRF)
- GIVEN a session idle 31 min THEN 401 (TestSessionIdleExpiry)
- GIVEN no reauth in 5 min WHEN PUT header THEN 403 reauth_required (TestSecretWriteRequiresReauth)
- GIVEN a header set WHEN GET server THEN the value is absent from the body and admin_events (TestSecretNeverEchoed)
- WHEN POST server with launcher=host THEN 422 (TestWebCannotAddHostStdio)
- GIVEN approve with a stale hash THEN 409 and tool stays pending (TestApproveStaleHashConflict)
- WHEN DELETE grant THEN the access token is rejected at once (TestRevokeGrantImmediate)
- WHEN demoting the last admin THEN 409 (TestCannotDemoteLastAdmin)
- WHEN any write succeeds THEN one admin_events row in the same tx (TestWritesAreAudited)
- GIVEN embedded HTML with inline scripts THEN CSP lists their hashes and no unsafe-inline (TestAdminCSPHashes)
- Existing `cmd/mcpgw` admin tests pass unchanged after the extraction (regression).

Go (`upstreamauth`, `internal/portalapi`):
- GIVEN a user_header server and a stored token WHEN a tool is called THEN the upstream gets `name: prefix+token` (TestUserHeaderCredentialSent)
- GIVEN a user_header server and no token WHEN a tool is called THEN ErrNotConnected with the portal URL (TestUserHeaderNotConnected)
- WHEN PUT token THEN GET connection returns last4 only and the token appears in no response or log (TestUserTokenWriteOnly)
- GIVEN the upstream answers 401 WHEN token/test THEN 422 upstream_rejected and nothing stored (TestTokenTestRejectedNotStored)
- GIVEN user A WHEN A reads or deletes B's connection THEN 404 (TestPortalCannotTouchOtherUser)
- GIVEN no ceiling on a server THEN it is absent from /portal/api/connections (TestPortalListsOnlyGrantedServers)
- GIVEN an admin revokes a connection THEN the next call is not connected and the user sees revoked_by_admin + reason (TestAdminRevokeConnection)
- GIVEN a refresh that fails with invalid_grant THEN status refresh_failed (TestRefreshFailureMarksStatus)
- GIVEN 60 servers THEN /portal/api/connections paginates and filters server side (TestPortalConnectionsPagination)

Web: Playwright smoke against `mcpgw serve` with the embedded build: login, approve a pending
tool, revoke a consent; portal: connect a user_header server with a token against a fake
upstream, see it as Conectada, disconnect.

## Out of scope

Pending actions / MRTR approvals for destructive calls (separate phase 2 item), editing stdio env
from the web, multi-tenant/org roles, dark-launch of host stdio, SSO.

## Addendum 2026-10-31 - stats, system info, connection probes (closes the G4 omissions)

Types: `web/admin/lib/stats-types.ts` (source of truth for the JSON below). All routes admin-only (`a.handle`), read-only, no audit rows, no secrets in responses.

- `GET /admin/api/stats?window=5m|15m|1h|24h|7d&server=<slug>&top=<n>` (default window 1h, top 10, max 200; bad value -> 422 field). Source `audit_events` (calls = rows, denied = decision 'deny', latency = latency_ms) and `executions` (executions, failed = status <> 'succeeded' and finished). Buckets: 5m->10s, 15m->30s, 1h->60s, 24h->900s, 7d->10800s, zero-filled and contiguous, oldest first; `p95_ms`/`p50_ms` via `percentile_cont` over non-null latency, null when no sample. `top_tools` ordered by calls desc, `top_denies` grouped by server/tool/reason ordered by count desc, `servers` ordered by calls desc. `server` filters everything (executions unaffected by server filter: totals.executions counts all).
- `GET /admin/api/system` -> `SystemInfo`: version/commit/commit_time/modified from `runtime/debug.ReadBuildInfo` (main module version and vcs.* settings; "(devel)" when unknown), go_version, the configured public URL, process start time.
- `POST /admin/api/servers/{slug}/test` -> 200 `ProbeResult` (404 unknown slug): list tools against the stored server with the SHARED credential when one exists, otherwise no credential; never writes (no catalog, no detection, no audit). 20 s timeout. 401/403 -> ok false, requires_auth true. `error` is a PT-BR, scrubbed message (never upstream body or credential).
- `POST /admin/api/servers/test {url, transport}` -> 200 `ProbeResult`: probe a URL before creating the server, no credential; URL validated with `admin.ValidateServerURL`; http transports only (stdio -> 422 field transport).

Screens: Overview gets the 24h traffic chart (series calls/denied), top tools and top denies; Logs gets period tabs (5m/15m/1h/24h), traffic chart, KPIs (calls, calls/min, denied, p95); server detail gets per-tool "uso 7d" and denied count (`/stats?window=7d&server=<slug>&top=200`) and a "Testar conexão" button; the add-server wizard gets "Testar URL"; Settings shows version, commit and uptime. Charts are inline SVG with attributes only (no `style`, no `popLayout`, G26).
