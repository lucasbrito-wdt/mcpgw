import { beforeAll, describe, expect, it } from 'vitest';
import { setCurrentLocale } from './i18n';
import {
  acceptsBothMethods,
  authMethodsOf,
  credentialEnvNote,
  credentialWordsOf,
  categoryLabel,
  chunkSlugs,
  connectionInEffectLabel,
  connectionStatusLabel,
  connectMenu,
  isValidProfile,
  profileQuery,
  keyOf,
  problemOf,
  problemReason,
  relationOf,
  tokenMatchesHint,
  type PortalApp,
} from './portal-types';

beforeAll(() => setCurrentLocale('pt-BR'));

const app = (patch: Partial<PortalApp>): PortalApp => ({
  slug: 'github',
  auth_kind: 'header',
  category: 'dev',
  connection: 'none',
  status: 'not_connected',
  account_label: null,
  last_used_at: null,
  tool_count: 3,
  level: 'read',
  ...patch,
});

describe('portal helpers', () => {
  it('maps statuses to PT-BR labels', () => {
    expect(connectionStatusLabel('revoked_by_admin')).toBe('Revogada pelo admin');
    expect(connectionStatusLabel('ready')).toBe('Pronta');
  });
  it('names the connection in effect', () => {
    expect(connectionInEffectLabel('personal')).toBe('Sua conexão');
    expect(connectionInEffectLabel('shared')).toBe('Conexão compartilhada (fornecida pelo admin)');
    expect(connectionInEffectLabel('none')).toBe('Não conectada');
  });
  it('offers "Para todos" only to admins and never for none', () => {
    expect(connectMenu({ auth_kind: 'header', is_admin: false })).toEqual(['personal']);
    expect(connectMenu({ auth_kind: 'oauth', is_admin: true })).toEqual(['personal', 'shared']);
    expect(connectMenu({ auth_kind: 'none', is_admin: true })).toEqual([]);
  });
  it('splits bulk disconnects in chunks of 100', () => {
    expect(chunkSlugs(Array.from({ length: 205 }, (_, i) => `s${i}`)).map((c) => c.length)).toEqual([100, 100, 5]);
  });
  it('checks the format hint client side before calling the API', () => {
    expect(tokenMatchesHint('github_pat_abc', 'github_pat_')).toBe(true);
    expect(tokenMatchesHint('ghp_abc', 'github_pat_')).toBe(false);
    expect(tokenMatchesHint('anything', '')).toBe(true);
  });
});

describe('portal view helpers', () => {
  it('groups apps by the viewer relation', () => {
    expect(relationOf(app({ connection: 'personal', status: 'connected' }))).toBe('own');
    expect(relationOf(app({ connection: 'personal', status: 'expired' }))).toBe('failing');
    expect(relationOf(app({ connection: 'shared', status: 'connected' }))).toBe('available');
    expect(relationOf(app({ auth_kind: 'none', status: 'ready' }))).toBe('available');
    expect(relationOf(app({}))).toBe('unconnected');
  });
  it('names the key a call resolves to', () => {
    expect(keyOf(app({ connection: 'personal' }))).toBe('personal');
    expect(keyOf(app({ connection: 'shared' }))).toBe('shared');
    expect(keyOf(app({}))).toBe('none');
    expect(keyOf(app({ auth_kind: 'none' }))).toBe('open');
  });
  it('maps failing statuses to a route problem', () => {
    expect(problemOf('revoked_by_admin')).toBe('revoked');
    expect(problemOf('refresh_failed')).toBe('expired');
    expect(problemOf('connected')).toBeUndefined();
    expect(problemReason('expired', 'Jira')).toBe('Sua chave do Jira expirou.');
  });
  it('labels categories, falling back to the slug', () => {
    expect(categoryLabel('dev')).toBe('Desenvolvimento');
    expect(categoryLabel('financeiro')).toBe('financeiro');
  });

  it('validates profile names like the server and builds the query', () => {
    expect(['default', 'acme', 'a', 'a_b-1'].every(isValidProfile)).toBe(true);
    expect(['', 'Upper', '1abc', 'a b', 'a'.repeat(33)].some(isValidProfile)).toBe(false);
    expect(profileQuery('default')).toBe('');
    expect(profileQuery('globex')).toBe('?profile=globex');
  });
});

describe('credentialEnvNote', () => {
  it('names the process variable the token goes to', () => {
    expect(credentialEnvNote('PORTAINER_API_KEY')).toBe('Seu token vai para a variável PORTAINER_API_KEY do processo.');
  });
});

describe('authMethodsOf', () => {
  it('derives the methods from auth_kind when the API sends none', () => {
    expect(authMethodsOf(app({ auth_kind: 'oauth' }))).toEqual(['oauth']);
    expect(authMethodsOf(app({ auth_kind: 'header' }))).toEqual(['token']);
    expect(authMethodsOf(app({ auth_kind: 'none' }))).toEqual([]);
  });

  it('trusts auth_methods when present, including an empty list', () => {
    expect(authMethodsOf(app({ auth_kind: 'oauth', auth_methods: ['oauth', 'token'] }))).toEqual(['oauth', 'token']);
    expect(authMethodsOf(app({ auth_kind: 'oauth', auth_methods: [] }))).toEqual([]);
  });

  it('flags servers that accept both', () => {
    expect(acceptsBothMethods(app({ auth_kind: 'oauth', auth_methods: ['oauth', 'token'] }))).toBe(true);
    expect(acceptsBothMethods(app({ auth_kind: 'header' }))).toBe(false);
  });
});

describe("credentialWordsOf", () => {
  it("reads the secret kind from the process variable name", () => {
    expect(credentialWordsOf("DATABASE_URI").label).toBe("Connection string");
    expect(credentialWordsOf("REDIS_PWD").label).toBe("Senha");
    expect(credentialWordsOf("KAFKA_SASL_PASSWORD").label).toBe("Senha");
    expect(credentialWordsOf("DOKPLOY_API_KEY").label).toBe("Token");
    expect(credentialWordsOf(null).label).toBe("Token");
    expect(credentialEnvNote("DATABASE_URI")).toBe("Sua connection string vai para a variável DATABASE_URI do processo.");
  });
});
