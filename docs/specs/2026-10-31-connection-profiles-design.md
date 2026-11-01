# Connection profiles - design

Status: approved, implemented
Date: 2026-10-31

## Context

A user may hold several accounts on the same upstream (Cloudflare: `default`, `acme`,
`globex`, mirroring `wrangler auth list`). Today `upstream_credentials` allows one personal row per
(server, user), so each account needs its own server (`cloudflare_acme`, ...): three
catalogs, three reviews, three ceilings for the same MCP.

## Decision

One server, several **named personal connections (profiles)** per user. The agent picks the profile
per call through an injected `profile` argument. Chosen by the user over per-profile namespaces and a
stateful `use_profile` tool.

Out of scope (v1): shared connections stay single (profile `default`); profiles for `auth_kind none`
servers (nothing to choose).

## Behavior (spec-delta)

ADDED
- WHEN a server has `auth_kind` oauth or header, THE SYSTEM SHALL expose every tool of that server to
  the agent with an extra optional `profile: string` argument, documented as "conexão a usar; sem
  valor usa default".
- IF the upstream tool already declares a `profile` property, THE SYSTEM SHALL inject `mcpgw_profile`
  instead for that tool, so upstream arguments are never shadowed.
- WHEN a call carries `profile`, THE SYSTEM SHALL remove it from the arguments before policy, audit
  redaction and forwarding, and use the user's active connection with that name.
- WHEN a call omits `profile`, THE SYSTEM SHALL use the user's `default` connection, else the shared
  connection (today's behavior).
- IF the named profile is not connected, THE SYSTEM SHALL deny with reason `not_connected` and a
  message listing the user's connected profile names and the portal URL.
- THE SYSTEM SHALL record the profile used in the audit event of the call.
- WHEN a user connects an OAuth or header server in the portal, THE SYSTEM SHALL accept an optional
  profile name (`^[a-z][a-z0-9_-]{0,31}$`, default `default`); connecting an existing name replaces it.
- THE portal SHALL list a server's connections per profile, with connect/disconnect per profile.

MODIFIED
- `upstream_credentials`: new column `profile text NOT NULL DEFAULT 'default'`; personal uniqueness
  becomes (server_id, user_id, profile); shared rows are always `default`.
- OAuth connect state carries the profile, so the callback stores the token under it.
- Ciphertext AAD: `default` keeps the current AAD (no re-seal of existing rows); other profiles bind
  the profile name into the AAD.

UNCHANGED
- Review hashes use the upstream definition before injection: adding profiles never re-quarantines.
- Policy (ceiling, consent) stays per server, not per profile.
- Upstream sessions are already keyed by credential, so each profile gets its own session.

## Acceptance tests (names become RED tests)

1. `TestProfileArgumentInjectedForAuthServers` - declarations of an oauth server's tool include
   `profile?: string`; an `auth_kind none` server's do not.
2. `TestProfileArgumentRenamedOnCollision` - a tool with its own `profile` property gets
   `mcpgw_profile`.
3. `TestCallUsesNamedProfile` - two profiles with distinct tokens; the upstream receives the token of
   the requested profile and does not receive the `profile` argument.
4. `TestCallWithoutProfileUsesDefault` - and falls back to shared when no personal default exists.
5. `TestUnknownProfileDeniedWithConnectedList` - denial lists connected profile names.
6. `TestDefaultProfileKeepsLegacyAAD` - a row sealed before the migration still opens.
7. `TestConnectStoresProfileFromState` - OAuth callback writes the row under the state's profile.
8. `TestPortalListsConnectionsPerProfile` and connect/disconnect per profile.
9. `TestAuditRecordsProfile`.

## Migration of the current setup

After it ships: connect `acme` and `globex` as profiles of `cloudflare`, then remove the
servers `cloudflare_acme` and `cloudflare_globex` (needs a remove path: CLI has none today).
