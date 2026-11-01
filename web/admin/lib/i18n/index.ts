// i18n API (en + pt-BR, no dependency). Static export: no locale routing; the client picks the locale.
//
// How to add a string
//  1. Pick or create lib/i18n/messages/<ns>.ts:
//       export const login = defineMessages({
//         en: { submit: 'Sign in', pending_one: '{n} pending', pending_other: '{n} pending' },
//         'pt-BR': { submit: 'Entrar', pending_one: '{n} pendente', pending_other: '{n} pendentes' },
//       });
//     pt-BR must have exactly the keys of en, or tsc fails. Interpolation: {name}. Plurals: key_one + key_other.
//  2. In a component:  const t = useT(login);  t('submit');  t.plural('pending', 3);  t('hello', { name })
//  3. In lib code (no hooks):  translate(getLocale(), login, 'submit')  or  translatePlural(getLocale(), login, 'pending', 3)
//  4. Dates and numbers: const f = useFormat(); f.date(iso, opts); f.number(n)
//     Non-React: formatDate(value, opts), formatNumber(n, opts) (active locale). Never hardcode 'pt-BR' in Intl calls.
//  5. Constants must not hold copy at module level (the locale is not known yet): use a function or a key.
//  6. Tests: wrap in <I18nProvider locale="pt-BR"> (renderToStaticMarkup) or call setCurrentLocale('pt-BR') for lib code.
//  7. Components that call lib label helpers (levelLabel...) should also call useLocale() so they re-render on a switch.
export {
  DEFAULT_LOCALE,
  LOCALES,
  LOCALE_NAMES,
  LOCALE_STORAGE_KEY,
  defineMessages,
  detectLocale,
  formatDate,
  formatNumber,
  getLocale,
  hasKey,
  isLocale,
  makeTranslator,
  setCurrentLocale,
  translate,
  translatePlural,
} from './core';
export type { Locale, Messages, Namespace, PluralKey, Translator, Vars } from './core';
export { I18nProvider, useFormat, useLocale, useT } from './react';
export type { I18nProviderProps, LocaleContextValue } from './react';
