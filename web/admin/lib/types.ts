// API-facing types used by the signature, shell and brand components.
// Replaces design/v5/src/data/types.ts (mock domain data is not ported).

/** Which key a call resolves to. Personal wins over shared; "open" needs no key. */
export type KeyKind = 'personal' | 'shared' | 'none' | 'open';

/** Why a personal connection is failing. */
export type ConnectionProblem = 'revoked' | 'expired' | 'scopes';

/** Where a call stopped on its route: before a key, at the key, at scopes, at the provider, or upstream. */
export type RouteCut = 'gateway' | 'key' | 'scopes' | 'provider' | 'upstream';

export type CallResult = 'ok' | 'denied' | 'error';

/** Which key a logged call used: own (personal), shared, open (no auth), missing (none). */
export type CallKey = 'own' | 'shared' | 'open' | 'missing';

/** Ceiling / tool access level as the API names it. */
export type AccessLevel = 'none' | 'read' | 'write' | 'destructive';

/** MCP client identifier, also the simple-icons slug used for its logo ("claude-code", "cursor"). */
export type AgentId = string;

export interface Agent {
  id: AgentId;
  name: string;
}

/** The part of a server the switchboard and route components need. */
export interface SwitchboardServer {
  slug: string;
  name: string;
  /** Key a call from the viewer uses right now. */
  key: KeyKind;
  /** Set when the viewer's personal key is broken. */
  problem?: ConnectionProblem;
}

/** `GET /<area>/api/me`. `is_admin` is only sent by the portal API. */
export interface Me {
  email: string;
  is_admin?: boolean;
}
