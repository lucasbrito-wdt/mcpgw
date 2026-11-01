// Types and pure helpers for the admin screens (servers, review, overview).
// Shapes follow the documented admin API JSON (plan wave-4a, Task 4.1); verify against the live API in 6.1.
import { useEffect, useState } from 'react';

import { api, ApiError } from './api';
import { getLocale, hasKey, translate } from './i18n';
import { labels } from './i18n/messages/labels';
import type { KeyKind } from './types';

export type AuthKind = 'none' | 'oauth' | 'header';
export type Transport = 'auto' | 'streamable_http' | 'sse' | 'stdio';
export type ServerStatus = 'active' | 'disabled';
export type ToolStatus = 'active' | 'pending_approval' | 'quarantined';

export interface HeaderConfig {
  name: string;
  prefix: string;
  format_hint: string;
  help_url: string;
}

export interface ServerSummary {
  slug: string;
  url: string;
  transport: Transport | string;
  auth_kind: AuthKind;
  category: string;
  status: ServerStatus | string;
  active_tools: number;
  pending_tools: number;
  has_shared_connection: boolean;
  has_oauth_client: boolean;
  /** stdio only: the env var each user's token is delivered in; null otherwise. */
  credential_env?: string | null;
  catalog_synced_at: string | null;
  trust_annotations: boolean;
  auto_approve_new_tools: boolean;
  /** In the admin list per plan gap G4; the example JSON of 4.1 does not show it yet. */
  last_used_at?: string | null;
}

export interface ServerDetail extends ServerSummary {
  header?: HeaderConfig | null;
  oauth_client_id?: string;
  stdio_launcher?: string;
  docker_image?: string;
  stdio_network?: boolean;
  /** Names of the fixed env vars, sorted; values never come back. */
  stdio_env_names?: string[];
  /** Variables each connection informs itself (a fixed one of the same name is the default); never values. */
  connection_env?: string[];
  detected_transport?: string;
  detected_protocol?: string;
}

export type AccessLevelSetting = 'read' | 'write' | 'destructive';

export interface Tool {
  server: string;
  name: string;
  level: AccessLevelSetting | string;
  overridden: boolean;
  status: ToolStatus | string;
  pending_changes: string[];
  hash: string;
}

export interface ToolDetail extends Tool {
  description: string;
  /** JSON object on the wire; rendered as text only. */
  input_schema: unknown;
  output_schema: unknown;
  annotations: unknown;
  approved_hash: string;
}

export interface Overview {
  servers: number;
  servers_disabled: number;
  tools_active: number;
  tools_quarantined: number;
  pending_approvals: number;
  denies_24h: number;
  executions_24h: number;
}

export interface SyncReport {
  added: string[];
  changed: string[];
  removed: string[];
  quarantined: string[];
}

export interface SharedConnection {
  status: string;
  last4: string;
  account_label: string | null;
  connected_by_email: string | null;
  last_used_at: string | null;
  expires_at: string | null;
  revoked_reason: string | null;
}

export interface ServerConnections {
  slug: string;
  shared: SharedConnection | null;
}

export interface AdminEvent {
  id: string;
  ts: string;
  actor_email: string;
  action: string;
  target: string;
  details: Record<string, unknown>;
}

export interface OAuthClientSummary {
  client_id: string;
  name: string;
}

export interface Paged<T> {
  items: T[];
  next_cursor: string;
}

export interface StdioEnvRow {
  name: string;
  value: string;
}

/** Fields of the add-server wizard, flat so each step edits its own part. */
export interface AddServerForm {
  slug: string;
  url?: string;
  auth_kind: AuthKind;
  transport: Transport;
  category?: string;
  docker_image?: string;
  /** Docker only: command run in the image (appended after it). */
  stdio_command?: string;
  stdio_network?: boolean;
  /** Fixed env of the process; rows with an empty name are dropped. Values are secrets. */
  stdio_env?: StdioEnvRow[];
  /** stdio only: when set, each user pastes their own token and it is injected under this name. */
  credential_env?: string;
  /** stdio with credential_env only: variables each connection informs itself. */
  connection_env?: string[];
  header_name?: string;
  header_prefix?: string;
  header_format_hint?: string;
  header_help_url?: string;
}

export interface AddServerBody {
  slug: string;
  url?: string;
  auth_kind: AuthKind;
  transport: Transport;
  category?: string;
  stdio_launcher?: 'docker';
  stdio_image?: string;
  stdio_command?: string;
  stdio_network?: boolean;
  stdio_env?: StdioEnvRow[];
  credential_env?: string;
  connection_env?: string[];
  /** stdio servers send only the two portal hints: no name or prefix applies to a process. */
  header?: { name?: string; prefix?: string; format_hint?: string; help_url?: string };
}

const DEFAULT_HEADER_NAME = 'Authorization';

function headerBody(form: AddServerForm): NonNullable<AddServerBody['header']> {
  const header: NonNullable<AddServerBody['header']> = {
    name: form.header_name?.trim() || DEFAULT_HEADER_NAME,
    prefix: form.header_prefix ?? '',
  };
  if (form.header_format_hint?.trim()) header.format_hint = form.header_format_hint.trim();
  if (form.header_help_url?.trim()) header.help_url = form.header_help_url.trim();
  return header;
}

function stdioBody(form: AddServerForm, body: AddServerBody): void {
  body.stdio_launcher = 'docker';
  body.stdio_image = form.docker_image?.trim() ?? '';
  if (form.stdio_command?.trim()) body.stdio_command = form.stdio_command.trim();
  body.stdio_network = form.stdio_network === true;
  const env = (form.stdio_env ?? []).filter((row) => row.name.trim() !== '').map((row) => ({ name: row.name.trim(), value: row.value }));
  if (env.length > 0) body.stdio_env = env;
  const credentialEnv = form.credential_env?.trim();
  if (!credentialEnv) {
    body.auth_kind = 'none';
    return;
  }
  body.auth_kind = 'header';
  body.credential_env = credentialEnv;
  if (form.connection_env?.length) body.connection_env = form.connection_env;
  const hints: NonNullable<AddServerBody['header']> = {};
  if (form.header_format_hint?.trim()) hints.format_hint = form.header_format_hint.trim();
  if (form.header_help_url?.trim()) hints.help_url = form.header_help_url.trim();
  if (Object.keys(hints).length > 0) body.header = hints;
}

/**
 * Request body of `POST /servers`. Never emits a host launcher. A stdio server is `none` (secrets in the fixed env)
 * or `header` through `credential_env`; header name and prefix only travel for HTTP header servers.
 */
export function buildAddServerBody(form: AddServerForm): AddServerBody {
  const body: AddServerBody = { slug: form.slug, auth_kind: form.auth_kind, transport: form.transport };
  if (form.category?.trim()) body.category = form.category.trim();
  if (form.transport === 'stdio') {
    stdioBody(form, body);
    return body;
  }
  if (form.url !== undefined) body.url = form.url.trim();
  if (form.auth_kind === 'header') body.header = headerBody(form);
  return body;
}

/** Unknown statuses are shown as the API sent them. */
function labelOr(group: string, id: string): string {
  const key = `${group}_${id}`;
  return hasKey(labels, key) ? translate(getLocale(), labels, key) : id;
}

export const serverStatusLabel = (status: string): string => labelOr('serverStatus', status);
export const toolStatusLabel = (status: string): string => labelOr('toolStatus', status);
export const authKindLabel = (kind: AuthKind): string => translate(getLocale(), labels, `authKind_${kind}`);

/** Key the gateway resolves for a server when nobody has a personal one: open, the shared key, or none. */
export function serverKeyKind(server: Pick<ServerSummary, 'auth_kind' | 'has_shared_connection'>): KeyKind {
  if (server.auth_kind === 'none') return 'open';
  return server.has_shared_connection ? 'shared' : 'none';
}

export function credentialSummary(server: Pick<ServerSummary, 'auth_kind' | 'has_shared_connection'>): string {
  if (server.auth_kind === 'none') return translate(getLocale(), labels, 'credential_none');
  return translate(getLocale(), labels, server.has_shared_connection ? 'credential_shared' : 'credential_noShared');
}

/** Pretty text of a definition part (schema, annotations). Always text: callers render it as a React text node. */
export function formatDefinition(value: unknown): string {
  if (value === null || value === undefined) return '';
  return typeof value === 'string' ? value : JSON.stringify(value, null, 2);
}

export interface LoadState<T> {
  data: T | undefined;
  error: Error | undefined;
  loading: boolean;
  reload: () => void;
}

interface Settled<T> {
  path: string;
  version: number;
  data?: T;
  error?: Error;
}

/**
 * GET `/admin/api<path>` on mount and whenever `path` changes. Keeps the last data of the same path while
 * `reload()` refetches; pass `null` to stay idle. Errors other than 401 (handled by `api`) come back in `error`.
 */
export function useLoad<T>(path: string | null): LoadState<T> {
  const [version, setVersion] = useState(0);
  const [settled, setSettled] = useState<Settled<T> | null>(null);

  useEffect(() => {
    if (path === null) return;
    let cancelled = false;
    api<T>('admin', path).then(
      (data) => {
        if (!cancelled) setSettled({ path, version, data });
      },
      (error: unknown) => {
        if (!cancelled) setSettled({ path, version, error: error instanceof Error ? error : new ApiError(0, 'unknown', String(error)) });
      },
    );
    return () => {
      cancelled = true;
    };
  }, [path, version]);

  const same = settled !== null && settled.path === path ? settled : null;
  return {
    data: same?.data,
    error: same?.version === version ? same.error : undefined,
    loading: path !== null && (same === null || same.version !== version),
    reload: () => setVersion((v) => v + 1),
  };
}
