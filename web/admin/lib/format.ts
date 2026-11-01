import { formatDate, getLocale, translate } from './i18n';
import { labels } from './i18n/messages/labels';
import type { AccessLevel, ConnectionProblem, KeyKind } from './types';

const DATE_TIME: Intl.DateTimeFormatOptions = {
  timeZone: 'America/Sao_Paulo',
  day: '2-digit',
  month: '2-digit',
  year: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
  hourCycle: 'h23',
};

/** A Record whose values follow the active locale at read time (the locale is unknown when the module loads). */
function localizedRecord<K extends string>(keys: readonly K[], label: (key: K) => string): Readonly<Record<K, string>> {
  const record = {} as Record<K, string>;
  for (const key of keys) Object.defineProperty(record, key, { enumerable: true, get: () => label(key) });
  return record;
}

export function maskToken(last4: string): string {
  return `••••${last4}`;
}

export function levelLabel(level: AccessLevel): string {
  return translate(getLocale(), labels, `level_${level}`);
}

export function formatDateTime(iso: string): string {
  return formatDate(iso, DATE_TIME).replace(', ', ' ');
}

export function truncate(text: string, max: number): string {
  return text.length > max ? `${text.slice(0, max)}…` : text;
}

export const KEY_LABEL = localizedRecord<KeyKind>(['personal', 'shared', 'none', 'open'], (key) => translate(getLocale(), labels, `key_${key}`));

export const PROBLEM_LABEL = localizedRecord<ConnectionProblem>(['revoked', 'expired', 'scopes'], (key) =>
  translate(getLocale(), labels, `problem_${key}`),
);

/** Two-letter avatar initials from the email's local part ("ana.souza@x" gives "AS"). */
export function initialsOf(email: string): string {
  const parts = email.split("@")[0].split(/[._-]+/).filter(Boolean);
  return ((parts[0]?.[0] ?? "?") + (parts[1]?.[0] ?? "")).toUpperCase();
}
