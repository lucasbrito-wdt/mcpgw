import { getLocale, translate } from './i18n';
import { errors } from './i18n/messages/errors';

export type Area = 'admin' | 'portal';

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public fields?: Record<string, string>,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

const CSRF_COOKIE: Record<Area, string> = { admin: 'mcpgw_admin_csrf', portal: 'mcpgw_portal_csrf' };

/** Reads the readable CSRF cookie: the `__Host-` name in production, the plain name in dev over http. */
export function csrfFromCookies(cookies: string, base: string): string {
  const jar = new Map<string, string>();
  for (const part of cookies.split(';')) {
    const eq = part.indexOf('=');
    if (eq > 0) jar.set(part.slice(0, eq).trim(), part.slice(eq + 1).trim());
  }
  return jar.get(`__Host-${base}`) ?? jar.get(base) ?? '';
}

interface ErrorEnvelope {
  error?: { code?: unknown; message?: unknown; fields?: unknown };
}

function isStringRecord(value: unknown): value is Record<string, string> {
  return typeof value === 'object' && value !== null && Object.values(value).every((v) => typeof v === 'string');
}

/** Maps the `{error: {code, message, fields}}` envelope; anything else becomes a generic error in the active locale. */
export function parseApiError(status: number, body: unknown): ApiError {
  const err = typeof body === 'object' && body !== null ? (body as ErrorEnvelope).error : undefined;
  const code = typeof err?.code === 'string' ? err.code : 'unknown';
  const message = typeof err?.message === 'string' && err.message !== '' ? err.message : translate(getLocale(), errors, 'generic');
  return new ApiError(status, code, message, isStringRecord(err?.fields) ? err.fields : undefined);
}

export async function api<T>(
  area: Area,
  path: string,
  init: { method?: string; body?: unknown } = {},
): Promise<T> {
  const method = init.method ?? 'GET';
  const headers: Record<string, string> = { Accept: 'application/json', 'Accept-Language': getLocale() };
  if (method !== 'GET') headers['X-CSRF-Token'] = csrfFromCookies(document.cookie, CSRF_COOKIE[area]);
  if (init.body !== undefined) headers['Content-Type'] = 'application/json';
  const res = await fetch(`/${area}/api${path}`, {
    method,
    headers,
    credentials: 'same-origin',
    body: init.body === undefined ? undefined : JSON.stringify(init.body),
  });
  if (res.status === 401 && !path.startsWith('/session')) {
    // Full navigation on purpose: the login page is a different static document, not a client route.
    // eslint-disable-next-line @next/next/no-location-assign-relative-destination
    window.location.assign(`/${area}/login/?next=${encodeURIComponent(location.pathname + location.search)}`);
  }
  if (res.status === 204) return undefined as T;
  const body: unknown = await res.json().catch(() => null);
  if (!res.ok) throw parseApiError(res.status, body);
  return body as T;
}
