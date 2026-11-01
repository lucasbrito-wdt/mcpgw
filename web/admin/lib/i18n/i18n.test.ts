import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  DEFAULT_LOCALE,
  I18nProvider,
  defineMessages,
  detectLocale,
  formatDate,
  formatNumber,
  getLocale,
  setCurrentLocale,
  translate,
  translatePlural,
  useT,
} from './index';
import { resolveClientLocale } from './core';

const demo = defineMessages({
  en: { hello: 'Hello, {name}!', file_one: '{n} file', file_other: '{n} files', raw: 'Keep {unknown}' },
  'pt-BR': { hello: 'Olá, {name}!', file_one: '{n} arquivo', file_other: '{n} arquivos', raw: 'Manter {unknown}' },
});

afterEach(() => {
  setCurrentLocale('en');
  vi.unstubAllGlobals();
});

describe('detectLocale', () => {
  it('prefers a stored valid locale', () => {
    expect(detectLocale('en', ['pt-BR'])).toBe('en');
    expect(detectLocale('pt-BR', ['en-US'])).toBe('pt-BR');
  });
  it('ignores an invalid stored value', () => {
    expect(detectLocale('fr', ['pt-PT'])).toBe('pt-BR');
    expect(detectLocale('fr', ['de'])).toBe('en');
  });
  it('maps any pt language to pt-BR and everything else to en', () => {
    expect(detectLocale(null, ['pt'])).toBe('pt-BR');
    expect(detectLocale(null, ['PT-br', 'en'])).toBe('pt-BR');
    expect(detectLocale(null, ['es-ES', 'fr'])).toBe('en');
    expect(detectLocale(null, [])).toBe('en');
  });
  it('picks the first supported language in the navigator list', () => {
    expect(detectLocale(null, ['en-US', 'pt-BR'])).toBe('en');
    expect(detectLocale(null, ['pt-BR', 'en'])).toBe('pt-BR');
    expect(detectLocale(null, ['fr', 'pt-BR'])).toBe('pt-BR');
    expect(detectLocale(null, ['fr', 'de'])).toBe('en');
  });
});

describe('translate', () => {
  it('interpolates {name} placeholders and leaves unknown ones', () => {
    expect(translate('en', demo, 'hello', { name: 'Ana' })).toBe('Hello, Ana!');
    expect(translate('pt-BR', demo, 'hello', { name: 'Ana' })).toBe('Olá, Ana!');
    expect(translate('en', demo, 'raw', { other: 1 })).toBe('Keep {unknown}');
  });
  it('picks plural forms with Intl.PluralRules', () => {
    expect(translatePlural('en', demo, 'file', 1)).toBe('1 file');
    expect(translatePlural('en', demo, 'file', 2)).toBe('2 files');
    expect(translatePlural('pt-BR', demo, 'file', 1)).toBe('1 arquivo');
    expect(translatePlural('pt-BR', demo, 'file', 5)).toBe('5 arquivos');
  });
  it('requires pt-BR to match the en keys', () => {
    // @ts-expect-error pt-BR is missing the `b` key
    defineMessages({ en: { a: 'a', b: 'b' }, 'pt-BR': { a: 'a' } });
    // @ts-expect-error pt-BR has an extra key
    defineMessages({ en: { a: 'a' }, 'pt-BR': { a: 'a', c: 'c' } });
  });
});

describe('current locale and formatting', () => {
  it('starts on the default locale and follows setCurrentLocale', () => {
    expect(getLocale()).toBe(DEFAULT_LOCALE);
    setCurrentLocale('pt-BR');
    expect(getLocale()).toBe('pt-BR');
  });
  it('formats numbers and dates with the active locale', () => {
    expect(formatNumber(1234.5, {}, 'en')).toBe('1,234.5');
    expect(formatNumber(1234.5, {}, 'pt-BR')).toBe('1.234,5');
    const month = { month: 'long', timeZone: 'UTC' } as const;
    expect(formatDate('2026-10-30T12:00:00Z', month)).toBe('October');
    setCurrentLocale('pt-BR');
    expect(formatDate('2026-10-30T12:00:00Z', month)).toBe('outubro');
  });
});

describe('I18nProvider', () => {
  const Probe = () => createElement('p', null, useT(demo)('hello', { name: 'Ana' }));

  it('renders the default locale without a pinned locale (static HTML)', () => {
    expect(renderToStaticMarkup(createElement(I18nProvider, null, createElement(Probe)))).toBe('<p>Hello, Ana!</p>');
  });
  it('renders a pinned locale', () => {
    expect(renderToStaticMarkup(createElement(I18nProvider, { locale: 'pt-BR' }, createElement(Probe)))).toBe('<p>Olá, Ana!</p>');
  });
  it('falls back to navigator languages when storage throws', () => {
    vi.stubGlobal('localStorage', {
      getItem: () => {
        throw new Error('blocked');
      },
    });
    vi.stubGlobal('navigator', { languages: ['pt-BR'] });
    expect(resolveClientLocale()).toBe('pt-BR');
  });
});
