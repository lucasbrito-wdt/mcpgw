import { beforeAll, describe, expect, it } from 'vitest';
import { setCurrentLocale } from './i18n';
import { connectionEnvBody, connectionEnvErrors, envInputType, parseEnvNames, connectionEnvNamesError } from './connection-env';

beforeAll(() => setCurrentLocale('pt-BR'));

const VARS = [
  { name: 'PORTAINER_URL', has_default: true },
  { name: 'PORTAINER_TEAM', has_default: false },
];

describe('envInputType', () => {
  it('uses url for *_URL and password otherwise', () => {
    expect(envInputType('PORTAINER_URL')).toBe('url');
    expect(envInputType('PORTAINER_TEAM')).toBe('password');
  });
});

describe('connectionEnvBody', () => {
  it('keeps the stored values when every field is blank on an existing connection', () => {
    expect(connectionEnvBody(VARS, { PORTAINER_URL: ' ' }, true)).toBeUndefined();
  });
  it('sends only the filled, trimmed values otherwise', () => {
    expect(connectionEnvBody(VARS, { PORTAINER_URL: ' https://a.example ', PORTAINER_TEAM: '' }, true)).toEqual({ PORTAINER_URL: 'https://a.example' });
    expect(connectionEnvBody(VARS, {}, false)).toEqual({});
  });
  it('sends nothing for a server without variables', () => {
    expect(connectionEnvBody([], {}, false)).toBeUndefined();
  });
});

describe('connectionEnvErrors', () => {
  it('requires a variable without default on a new connection', () => {
    expect(connectionEnvErrors(VARS, {}, false)).toEqual({ PORTAINER_TEAM: 'Informe PORTAINER_TEAM: o servidor não tem valor padrão.' });
  });
  it('accepts blank fields on an existing connection (values kept)', () => {
    expect(connectionEnvErrors(VARS, {}, true)).toEqual({});
  });
  it('rejects line breaks and long values', () => {
    const errors = connectionEnvErrors(VARS, { PORTAINER_TEAM: 'a\nb', PORTAINER_URL: 'x'.repeat(2049) }, false);
    expect(Object.keys(errors).sort()).toEqual(['PORTAINER_TEAM', 'PORTAINER_URL']);
  });
});

describe('parseEnvNames', () => {
  it('splits on commas and spaces and drops blanks', () => {
    expect(parseEnvNames(' PORTAINER_URL, PORTAINER_TEAM  ,')).toEqual(['PORTAINER_URL', 'PORTAINER_TEAM']);
    expect(parseEnvNames('')).toEqual([]);
  });
});

describe('connectionEnvNamesError', () => {
  it('mirrors the server rules', () => {
    expect(connectionEnvNamesError('PORTAINER_URL', 'PORTAINER_API_KEY')).toBeUndefined();
    expect(connectionEnvNamesError('', '')).toBeUndefined();
    expect(connectionEnvNamesError('portainer_url', 'KEY')).toMatch(/inválido/);
    expect(connectionEnvNamesError('A, A', 'KEY')).toMatch(/repetida/);
    expect(connectionEnvNamesError('KEY', 'KEY')).toMatch(/token/);
    expect(connectionEnvNamesError('A', '')).toMatch(/credencial/);
  });
});
