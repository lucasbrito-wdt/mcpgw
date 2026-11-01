// Fixed environment of a stdio server, edited after creation. Values are write-only secrets: the API
// lists only the names, and an edit is a merge (`set` adds or replaces, `unset` removes).
import { getLocale, translate, type Vars } from './i18n';
import { validation } from './i18n/messages/validation';

const message = (key: keyof typeof validation.en, vars?: Vars): string => translate(getLocale(), validation, key, vars);

export const MAX_STDIO_ENV_VALUE = 4096;
const ENV_NAME_PATTERN = /^[A-Z_][A-Z0-9_]{0,63}$/;

export interface StdioEnvFieldErrors {
  name?: string;
  value?: string;
}

/** Client-side mirror of the server checks for one variable to add or replace. */
export function stdioEnvError(name: string, value: string, credentialEnv: string): StdioEnvFieldErrors {
  const errors: StdioEnvFieldErrors = {};
  const trimmed = name.trim();
  if (!ENV_NAME_PATTERN.test(trimmed)) errors.name = message('stdioNameFormat');
  else if (trimmed === credentialEnv.trim()) errors.name = message('stdioNameIsToken', { name: trimmed });
  if (value.trim() === '') errors.value = message('stdioValueRequired');
  else if (value.length > MAX_STDIO_ENV_VALUE || /[\r\n]/.test(value)) errors.value = message('valueLimit', { max: MAX_STDIO_ENV_VALUE });
  return errors;
}

/** The `stdio_env` of `PATCH /servers/{slug}`. */
export const stdioEnvSetBody = (name: string, value: string) => ({ stdio_env: { set: { [name.trim()]: value } } });
export const stdioEnvUnsetBody = (name: string) => ({ stdio_env: { unset: [name] } });
