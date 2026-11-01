import { afterEach, describe, expect, it, vi } from 'vitest';
import { api, csrfFromCookies, parseApiError, ApiError } from './api';
import { setCurrentLocale } from './i18n';

afterEach(() => {
  setCurrentLocale('en');
  vi.unstubAllGlobals();
});

describe('csrfFromCookies', () => {
  it('reads the __Host- cookie and falls back to the plain dev name', () => {
    expect(csrfFromCookies('a=1; __Host-mcpgw_admin_csrf=tok', 'mcpgw_admin_csrf')).toBe('tok');
    expect(csrfFromCookies('mcpgw_admin_csrf=dev', 'mcpgw_admin_csrf')).toBe('dev');
    expect(csrfFromCookies('', 'mcpgw_admin_csrf')).toBe('');
  });
});

describe('parseApiError', () => {
  it('maps the error envelope', () => {
    const e = parseApiError(422, { error: { code: 'invalid', message: 'slug inválido', fields: { slug: 'slug inválido' } } });
    expect(e).toBeInstanceOf(ApiError);
    expect([e.status, e.code, e.fields?.slug]).toEqual([422, 'invalid', 'slug inválido']);
  });
  it('falls back to a generic message in the active locale on a non-JSON body', () => {
    expect(parseApiError(502, 'bad gateway').message).toBe('Unexpected error. Try again.');
    setCurrentLocale('pt-BR');
    expect(parseApiError(502, 'bad gateway').message).toBe('Erro inesperado. Tente novamente.');
  });
});

describe('api', () => {
  it('sends the active locale as Accept-Language', async () => {
    const fetchMock = vi.fn(async () => new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    vi.stubGlobal('document', { cookie: '' });
    setCurrentLocale('pt-BR');
    await api('portal', '/me');
    const init = (fetchMock.mock.calls[0] as unknown as [string, RequestInit])[1];
    expect((init.headers as Record<string, string>)['Accept-Language']).toBe('pt-BR');
  });
});
