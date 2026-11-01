# Security Policy

mcpgw stores upstream credentials, issues OAuth tokens and runs model-written code, so security reports are taken seriously.

## Supported versions

Security fixes are made for:

| Version | Supported |
|---------|-----------|
| Latest release | Yes |
| `main` | Yes |
| Older releases | No; upgrade to the latest release |

## Reporting a vulnerability

Do not open a public issue, pull request or discussion for a vulnerability.

Report it privately through GitHub private vulnerability reporting:

<https://github.com/lucasbrito-wdt/mcpgw/security/advisories/new>

Only the maintainers can see the report until a fix is released and the advisory is published.

### What to include

The more of this you can provide, the faster the report can be triaged:

- A description of the issue and its impact: what an attacker gains and what they need to start (an unauthenticated request, a portal user account, an admin session, a malicious upstream MCP server, model-written code in code mode).
- The affected component and the version or commit (the admin UI Configurações page shows both under "Versão" and "Commit"; `git rev-parse HEAD` works for a checkout).
- Step-by-step reproduction, or a proof of concept. A failing test against the repository is ideal.
- Your configuration where relevant: transport (HTTP or stdio), auth kind of the upstream server, whether `MCPGW_PUBLIC_URL` is http or https.
- Any suggested fix or mitigation.
- Whether and how you would like to be credited.

Do not include real credentials, master keys or data belonging to other people in the report.

### What to expect

This is a volunteer-maintained project, so the following is a best-effort goal, not a guarantee:

- Acknowledgement of your report within 7 days.
- An initial assessment (confirmed, needs more information, or not a vulnerability) within 14 days.
- Updates as the fix progresses. The time to a fix depends on severity and complexity.
- Coordinated disclosure: the advisory is published when a fix is available, and you are credited unless you prefer otherwise. Please give us a reasonable chance to ship the fix before you disclose publicly.

## Scope

Reports about the gateway code in this repository are in scope. These areas are of particular interest:

| Area | Where | Examples |
|------|-------|----------|
| Credential storage and encryption | `internal/vault`, `internal/upstreamauth` | Weaknesses in the AES-256-GCM envelope encryption or key derivation from `MCPGW_MASTER_KEY`; a sealed secret that can be opened in the wrong context; plaintext secrets reaching the database, logs or API responses |
| OAuth flows | `internal/oauth`, `internal/upstreamauth` | Flaws in the authorization server that MCP clients use (authorization, consent, token issue and refresh, refresh-token reuse handling, client registration); redirect or PKCE bypass; SSRF through client metadata or upstream OAuth discovery |
| Sandbox escape from code mode | `internal/sandbox`, `internal/codemode` | Model-written JavaScript escaping the QuickJS/WebAssembly sandbox, reading host files or environment, reaching the network, exhausting host resources, or obtaining gateway or upstream credentials; bypass of the per-call policy check or secret scrubbing |
| Auth bypass in admin or portal | `internal/adminapi`, `internal/portalapi` | Reaching admin or portal API routes without a valid session; CSRF or `Origin` check bypass; a non-admin performing admin actions; one user reading or changing another user's connections or credentials |
