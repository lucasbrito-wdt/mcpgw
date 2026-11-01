"use client";

import { createContext, useContext, useEffect, useMemo, useSyncExternalStore, type ReactNode } from "react";

import {
  DEFAULT_LOCALE,
  changeLocale,
  formatDate,
  formatNumber,
  makeTranslator,
  readLocaleSnapshot,
  setCurrentLocale,
  subscribeLocale,
  type Locale,
  type Messages,
  type Namespace,
  type Translator,
} from "./core";

export interface LocaleContextValue {
  locale: Locale;
  setLocale: (locale: Locale) => void;
}

const LocaleContext = createContext<LocaleContextValue>({ locale: DEFAULT_LOCALE, setLocale: changeLocale });

export interface I18nProviderProps {
  children?: ReactNode;
  /** Pins the locale and skips detection. For tests and previews only. */
  locale?: Locale;
}

/**
 * The static HTML renders DEFAULT_LOCALE; on the client useSyncExternalStore re-renders with the stored or
 * detected locale right after hydration (no mismatch warning), then mirrors it into <html lang>.
 */
export function I18nProvider({ children, locale: pinned }: I18nProviderProps) {
  const detected = useSyncExternalStore(subscribeLocale, readLocaleSnapshot, () => DEFAULT_LOCALE);
  const locale = pinned ?? detected;

  useEffect(() => {
    if (pinned) setCurrentLocale(pinned);
  }, [pinned]);
  useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);

  const value = useMemo<LocaleContextValue>(() => ({ locale, setLocale: changeLocale }), [locale]);
  return <LocaleContext.Provider value={value}>{children}</LocaleContext.Provider>;
}

export function useLocale(): LocaleContextValue {
  return useContext(LocaleContext);
}

export function useT<E extends Messages>(ns: Namespace<E>): Translator<E> {
  const { locale } = useLocale();
  return useMemo(() => makeTranslator(locale, ns), [locale, ns]);
}

/** Date and number formatters bound to the active locale. */
export function useFormat() {
  const { locale } = useLocale();
  return useMemo(
    () => ({
      locale,
      date: (value: Date | string | number, options?: Intl.DateTimeFormatOptions) => formatDate(value, options, locale),
      number: (value: number, options?: Intl.NumberFormatOptions) => formatNumber(value, options, locale),
    }),
    [locale],
  );
}
