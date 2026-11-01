import type { ConnectionVar } from './connection-env';
import { getLocale, hasKey, translate } from './i18n';
import { labels } from './i18n/messages/labels';
import type { AccessLevel, ConnectionProblem, KeyKind } from './types';

export type { Me } from './types';

/** How the upstream accepts a credential (`static_header` rows are already answered as `header`). */
export type AuthKind = 'none' | 'oauth' | 'header';

/** One way a viewer can connect to a server: log in at the provider, or paste a token. */
export type AuthMethod = 'oauth' | 'token';

/** Which connection a call resolves to for the viewer: personal wins over shared. */
export type ConnectionInEffect = 'personal' | 'shared' | 'none';

export type ConnectionStatus =
  | 'connected'
  | 'expiring'
  | 'expired'
  | 'refresh_failed'
  | 'revoked_by_admin'
  | 'scopes_changed'
  | 'not_connected'
  | 'ready';

/** Who a new credential serves: only the viewer, or everybody without a personal one (admins only). */
export type ConnectAudience = 'personal' | 'shared';

/** The viewer's relation to a server; drives the portal groups and tabs. */
export type Relation = 'own' | 'failing' | 'available' | 'unconnected';

/** One personal connection of the viewer to a server; the token itself never comes, only `last4`. */
export interface PortalProfile {
  profile: string;
  status: ConnectionStatus;
  last4: string | null;
  account_label: string | null;
  last_used_at: string | null;
  connected_at: string;
  expires_at: string | null;
  /** Per-connection variables this connection has a value for (names only, never values). */
  env_set?: string[];
  /** How this connection was made; absent on an older API. */
  method?: AuthMethod;
  /** Host of this connection's own MCP URL (servers with `connection_url`); never the full URL. */
  url_host?: string | null;
}

export const DEFAULT_PROFILE = 'default';

/** Same rule as the server (`^[a-z][a-z0-9_-]{0,31}$`); the server checks again. */
export const PROFILE_PATTERN = /^[a-z][a-z0-9_-]{0,31}$/;

export function isValidProfile(name: string): boolean {
  return PROFILE_PATTERN.test(name);
}

/** Query string that selects a profile on the write endpoints; the default profile needs none. */
export function profileQuery(profile: string): string {
  return profile === DEFAULT_PROFILE ? '' : `?profile=${encodeURIComponent(profile)}`;
}

/** One row of `GET /portal/api/connections`. `name` is optional: the contract carries the slug only. */
export interface PortalApp {
  slug: string;
  name?: string;
  auth_kind: AuthKind;
  /** Every method the viewer can pick; absent on an older API (use `authMethodsOf`). */
  auth_methods?: AuthMethod[];
  category: string | null;
  connection: ConnectionInEffect;
  status: ConnectionStatus;
  account_label: string | null;
  last_used_at: string | null;
  tool_count: number;
  level: AccessLevel;
  /** stdio servers: the env var of the process the token is passed in (no HTTP header, no token test). */
  credential_env?: string | null;
  /** stdio servers: variables each connection informs itself; `has_default` when the server has a fixed one. */
  connection_env?: ConnectionVar[];
  /** Each connection informs its own MCP URL (`url` on the token write and test). */
  connection_url?: boolean;
  /** The viewer's own connections, default first; shared info stays in the fields above. */
  profiles?: PortalProfile[];
}

export type ConnectionsSummary = Partial<Record<ConnectionStatus, number>>;

export interface ConnectionsPage {
  summary: ConnectionsSummary;
  items: PortalApp[];
  next_cursor?: string | null;
}

export interface PortalTool {
  name: string;
  level: AccessLevel;
  /** Upstream text, untrusted: render as a text node only. */
  description?: string;
}

export interface TimelineEvent {
  ts: string;
  event: string;
}

/** `GET /portal/api/connections/{slug}`. Token values never appear here, only `last4`. */
export interface PortalAppDetail extends Omit<PortalApp, 'last_used_at' | 'tool_count' | 'level'> {
  last4?: string | null;
  shared_available: boolean;
  /** Admins only. */
  shared_last4?: string | null;
  expires_at: string | null;
  scope_granted: string;
  help_url: string | null;
  format_hint: string | null;
  timeline: TimelineEvent[];
  tools: PortalTool[];
  revoked_reason: string | null;
}

export const BULK_LIMIT = 100;

const PROBLEM_OF: Partial<Record<ConnectionStatus, ConnectionProblem>> = {
  expired: 'expired',
  refresh_failed: 'expired',
  revoked_by_admin: 'revoked',
  scopes_changed: 'scopes',
};

export function connectionStatusLabel(status: ConnectionStatus): string {
  return translate(getLocale(), labels, `connection_${status}`);
}

export function connectionInEffectLabel(connection: ConnectionInEffect): string {
  return translate(getLocale(), labels, `inEffect_${connection}`);
}

/** Menu of the "Conectar conta" button. "Para todos" exists only for admins; `none` has nothing to connect. */
export function connectMenu(input: { auth_kind: AuthKind; is_admin: boolean }): ConnectAudience[] {
  if (input.auth_kind === 'none') return [];
  return input.is_admin ? ['personal', 'shared'] : ['personal'];
}

const METHODS_OF_KIND: Record<AuthKind, AuthMethod[]> = { none: [], oauth: ['oauth'], header: ['token'] };

/** Methods the server accepts; derives them from `auth_kind` when the API predates `auth_methods`. */
export function authMethodsOf(app: Pick<PortalApp, 'auth_kind' | 'auth_methods'>): AuthMethod[] {
  return app.auth_methods ?? METHODS_OF_KIND[app.auth_kind];
}

export function acceptsBothMethods(app: Pick<PortalApp, 'auth_kind' | 'auth_methods'>): boolean {
  return authMethodsOf(app).length > 1;
}

export function chunkSlugs(slugs: readonly string[], size = BULK_LIMIT): string[][] {
  const chunks: string[][] = [];
  for (let i = 0; i < slugs.length; i += size) chunks.push(slugs.slice(i, i + size));
  return chunks;
}

/** Client side shape check; the server checks again. An empty hint accepts anything. */
export function tokenMatchesHint(token: string, hint: string): boolean {
  return token.trim().startsWith(hint);
}

export type CredentialKind = "token" | "password" | "uri";

/** UI wording for one kind of secret: label, "the x", "your x", and the gender suffix of its PT-BR adjectives. */
export interface CredentialWords {
  kind: CredentialKind;
  label: string;
  the: string;
  your: string;
  suffix: "o" | "a";
}

const SECRET_SUFFIX: Record<CredentialKind, "o" | "a"> = { token: "o", password: "a", uri: "a" };

function credentialWords(kind: CredentialKind): CredentialWords {
  const locale = getLocale();
  return {
    kind,
    label: translate(locale, labels, `secret_${kind}_label`),
    the: translate(locale, labels, `secret_${kind}_the`),
    your: translate(locale, labels, `secret_${kind}_your`),
    suffix: SECRET_SUFFIX[kind],
  };
}

/** What the secret is, read from the process variable it goes to (DATABASE_URI, REDIS_PWD); header servers always take a token. */
export function credentialWordsOf(envName?: string | null): CredentialWords {
  if (envName && /(_URI|_URL|_DSN)$/i.test(envName)) return credentialWords("uri");
  if (envName && /(PWD|PASS|PASSWORD)$/i.test(envName)) return credentialWords("password");
  return credentialWords("token");
}

/** Shown in dialogs and the guide in place of any header wording when the secret goes to a process variable. */
export const credentialEnvNote = (envName: string): string =>
  translate(getLocale(), labels, "credentialEnvNote", { your: credentialWordsOf(envName).your, env: envName });

export function categoryLabel(category: string): string {
  const key = `category_${category}`;
  return hasKey(labels, key) ? translate(getLocale(), labels, key) : category;
}

/** Display name: the API name when it has one, otherwise the slug in title case ("gitlab-ci" -> "Gitlab Ci"). */
export function appName(app: Pick<PortalApp, 'slug' | 'name'>): string {
  if (app.name) return app.name;
  return app.slug
    .split(/[-_]+/)
    .filter(Boolean)
    .map((word) => word[0].toUpperCase() + word.slice(1))
    .join(' ');
}

export function problemOf(status: ConnectionStatus): ConnectionProblem | undefined {
  return PROBLEM_OF[status];
}

export function problemReason(status: ConnectionStatus, name: string): string {
  switch (status) {
    case 'expired':
    case 'refresh_failed':
    case 'revoked_by_admin':
    case 'scopes_changed':
      return translate(getLocale(), labels, `reason_${status}`, { name });
    default:
      return '';
  }
}

export function keyOf(app: Pick<PortalApp, 'auth_kind' | 'connection'>): KeyKind {
  return app.auth_kind === 'none' ? 'open' : app.connection;
}

export function relationOf(app: Pick<PortalApp, 'auth_kind' | 'connection' | 'status'>): Relation {
  if (app.auth_kind === 'none') return 'available';
  if (app.connection === 'shared') return 'available';
  if (app.connection === 'none') return 'unconnected';
  return problemOf(app.status) ? 'failing' : 'own';
}

export function isHttpsUrl(value: string | null | undefined): value is string {
  if (!value) return false;
  try {
    return new URL(value).protocol === 'https:';
  } catch {
    return false;
  }
}
