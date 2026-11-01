// Per-connection environment variables of a stdio server: the admin declares the names, each
// connection (profile) informs its own values. Values are secrets: they never come back from the API.
import { getLocale, translate, type Vars } from './i18n';
import { validation } from './i18n/messages/validation';

const message = (key: keyof typeof validation.en, vars?: Vars): string => translate(getLocale(), validation, key, vars);

/** A variable the server declares; `has_default` says the fixed env has one (its value is never shown). */
export interface ConnectionVar {
  name: string;
  has_default: boolean;
}

export const MAX_ENV_VALUE = 2048;
const MAX_CONNECTION_ENV = 16;
const ENV_NAME_PATTERN = /^[A-Z_][A-Z0-9_]{0,63}$/;

/** URL variables get a url input; anything else is treated as a secret. */
export const envInputType = (name: string): 'url' | 'password' => (name.endsWith('_URL') ? 'url' : 'password');

const isBlank = (value: string | undefined): boolean => (value ?? '').trim() === '';

/** True when an existing connection keeps its stored values: every field was left blank. */
const keepsStored = (vars: readonly ConnectionVar[], values: Record<string, string>, isExisting: boolean): boolean =>
  isExisting && vars.every((v) => isBlank(values[v.name]));

/**
 * The `env` of `PUT .../token`: undefined keeps the stored values (no variables, or all blank on an existing
 * connection); otherwise the filled values, which replace the stored ones (a blank one falls back to the default).
 */
export function connectionEnvBody(vars: readonly ConnectionVar[], values: Record<string, string>, isExisting: boolean): Record<string, string> | undefined {
  if (vars.length === 0 || keepsStored(vars, values, isExisting)) return undefined;
  const body: Record<string, string> = {};
  for (const v of vars) if (!isBlank(values[v.name])) body[v.name] = values[v.name].trim();
  return body;
}

/** Client-side mirror of the server checks, by variable name. */
export function connectionEnvErrors(vars: readonly ConnectionVar[], values: Record<string, string>, isExisting: boolean): Record<string, string> {
  const errors: Record<string, string> = {};
  if (keepsStored(vars, values, isExisting)) return errors;
  for (const v of vars) {
    const value = (values[v.name] ?? '').trim();
    if (value === '' && !v.has_default) errors[v.name] = message('envRequired', { name: v.name });
    else if (value.length > MAX_ENV_VALUE || /[\r\n]/.test(value)) errors[v.name] = message('valueLimit', { max: MAX_ENV_VALUE });
  }
  return errors;
}

/** Admin input "PORTAINER_URL, OTHER" -> names. */
export const parseEnvNames = (raw: string): string[] => raw.split(/[\s,]+/).filter(Boolean);

/** Client-side mirror of the admin validation of `connection_env`; undefined when fine. */
export function connectionEnvNamesError(raw: string, credentialEnv: string): string | undefined {
  const names = parseEnvNames(raw);
  if (names.length === 0) return undefined;
  if (!credentialEnv.trim()) return message('envNamesNeedCredential');
  if (names.length > MAX_CONNECTION_ENV) return message('envNamesMax', { max: MAX_CONNECTION_ENV });
  const bad = names.find((name) => !ENV_NAME_PATTERN.test(name));
  if (bad) return message('envNameInvalid', { name: bad });
  const dup = names.find((name, i) => names.indexOf(name) !== i);
  if (dup) return message('envNameDuplicate', { name: dup });
  return names.includes(credentialEnv.trim()) ? message('envNameIsToken', { name: credentialEnv.trim() }) : undefined;
}
