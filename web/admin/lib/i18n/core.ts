// Pure i18n core: no React, safe to import from lib code and server modules.

export type Locale = 'en' | 'pt-BR';
export const LOCALES: readonly Locale[] = ['en', 'pt-BR'];
export const DEFAULT_LOCALE: Locale = 'en';
export const LOCALE_STORAGE_KEY = 'mcpgw.locale';
/** Endonyms: shown the same in every locale. */
export const LOCALE_NAMES: Record<Locale, string> = { en: 'English', 'pt-BR': 'Português (Brasil)' };

export const isLocale = (value: unknown): value is Locale => LOCALES.includes(value as Locale);

/** A stored valid locale wins; otherwise the first navigator language that is `pt*` (pt-BR) or `en*` (en) decides; else en. */
export function detectLocale(stored: string | null, languages: readonly string[]): Locale {
  if (isLocale(stored)) return stored;
  for (const language of languages) {
    const lower = language.toLowerCase();
    if (lower.startsWith('pt')) return 'pt-BR';
    if (lower.startsWith('en')) return 'en';
  }
  return DEFAULT_LOCALE;
}

/** Stored choice plus navigator languages. Every browser API is guarded: storage can throw (privacy modes). */
export function resolveClientLocale(): Locale {
  let stored: string | null = null;
  try {
    stored = localStorage.getItem(LOCALE_STORAGE_KEY);
  } catch {
    // Storage blocked: fall through to the navigator languages.
  }
  const languages = typeof navigator === 'undefined' ? [] : (navigator.languages ?? [navigator.language]);
  return detectLocale(stored, languages);
}

// Messages ----------------------------------------------------------------------------------------

export type Messages = Record<string, string>;
export type Vars = Record<string, string | number>;

/** One namespace: pt-BR must have exactly the keys of en (missing or extra key is a type error). */
export interface Namespace<E extends Messages> {
  readonly en: E;
  readonly 'pt-BR': Record<keyof E & string, string>;
}

export function defineMessages<E extends Messages>(ns: { en: E; 'pt-BR': Record<keyof NoInfer<E> & string, string> }): Namespace<E> {
  return ns;
}

/** Base names `k` that have both `k_one` and `k_other`. */
export type PluralKey<E extends Messages> = {
  [K in keyof E & string]: K extends `${infer B}_one` ? (`${B}_other` extends keyof E ? B : never) : never;
}[keyof E & string];

export function hasKey<E extends Messages>(ns: Namespace<E>, key: string): key is keyof E & string {
  return Object.hasOwn(ns.en, key);
}

function interpolate(template: string, vars?: Vars): string {
  if (!vars) return template;
  return template.replace(/\{(\w+)\}/g, (placeholder, name: string) => (name in vars ? String(vars[name]) : placeholder));
}

export function translate<E extends Messages>(locale: Locale, ns: Namespace<E>, key: keyof E & string, vars?: Vars): string {
  return interpolate(ns[locale][key] ?? ns.en[key] ?? key, vars);
}

const pluralRules = new Map<Locale, Intl.PluralRules>();

/** Picks `${key}_one` / `${key}_other` (or another CLDR category when the namespace defines it); `{n}` is filled in. */
export function translatePlural<E extends Messages>(locale: Locale, ns: Namespace<E>, key: PluralKey<E>, n: number, vars?: Vars): string {
  let rules = pluralRules.get(locale);
  if (!rules) pluralRules.set(locale, (rules = new Intl.PluralRules(INTL_TAG[locale])));
  const category = `${key}_${rules.select(n)}`;
  const picked = (hasKey(ns, category) ? category : `${key}_other`) as keyof E & string;
  return translate(locale, ns, picked, { n, ...vars });
}

export interface Translator<E extends Messages> {
  (key: keyof E & string, vars?: Vars): string;
  plural(key: PluralKey<E>, n: number, vars?: Vars): string;
}

export function makeTranslator<E extends Messages>(locale: Locale, ns: Namespace<E>): Translator<E> {
  const t = (key: keyof E & string, vars?: Vars) => translate(locale, ns, key, vars);
  t.plural = (key: PluralKey<E>, n: number, vars?: Vars) => translatePlural(locale, ns, key, n, vars);
  return t;
}

// Current locale (module store) ---------------------------------------------------------------------
// Lets non-React code (lib/api.ts, label helpers) read the active locale. I18nProvider subscribes to it.

let current: Locale = DEFAULT_LOCALE;
let resolved = false;
const listeners = new Set<() => void>();

export const getLocale = (): Locale => current;

/** Sets the active locale and notifies the provider. Tests call it directly to pin a locale. */
export function setCurrentLocale(locale: Locale): void {
  resolved = true;
  if (locale === current) return;
  current = locale;
  listeners.forEach((listener) => listener());
}

export function subscribeLocale(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** Client snapshot: resolves the browser locale once, on first read. The server snapshot is DEFAULT_LOCALE. */
export function readLocaleSnapshot(): Locale {
  if (!resolved && typeof window !== 'undefined') {
    resolved = true;
    current = resolveClientLocale();
  }
  return current;
}

/** Persists and applies a user choice. */
export function changeLocale(locale: Locale): void {
  try {
    localStorage.setItem(LOCALE_STORAGE_KEY, locale);
  } catch {
    // Storage blocked: the choice still applies for this page view.
  }
  setCurrentLocale(locale);
}

// Formatting ----------------------------------------------------------------------------------------

const INTL_TAG: Record<Locale, string> = { en: 'en-US', 'pt-BR': 'pt-BR' };
const formatters = new Map<string, Intl.DateTimeFormat | Intl.NumberFormat>();

function cached<T extends Intl.DateTimeFormat | Intl.NumberFormat>(kind: string, locale: Locale, options: object, create: (tag: string) => T): T {
  const id = `${kind}|${locale}|${JSON.stringify(options)}`;
  let formatter = formatters.get(id);
  if (!formatter) formatters.set(id, (formatter = create(INTL_TAG[locale])));
  return formatter as T;
}

export function formatDate(value: Date | string | number, options: Intl.DateTimeFormatOptions = { dateStyle: 'medium' }, locale: Locale = current): string {
  return cached('date', locale, options, (tag) => new Intl.DateTimeFormat(tag, options)).format(new Date(value));
}

export function formatNumber(value: number, options: Intl.NumberFormatOptions = {}, locale: Locale = current): string {
  return cached('number', locale, options, (tag) => new Intl.NumberFormat(tag, options)).format(value);
}
