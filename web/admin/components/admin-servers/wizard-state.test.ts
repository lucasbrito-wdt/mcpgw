import { beforeAll, describe, expect, it } from 'vitest';
import { setCurrentLocale } from '../../lib/i18n';
import { EMPTY_HEADER, EMPTY_SERVER, headerDraftErrors, serverDraftErrors, startsWithPrefix, stdioAuthKind, stripPrefix } from './wizard-state';

describe('wizard-state', () => {
  beforeAll(() => setCurrentLocale('pt-BR'));
  it('detects and strips a prefix already inside the value', () => {
    expect(startsWithPrefix('Bearer abc', 'Bearer ')).toBe(true);
    expect(startsWithPrefix('abc', 'Bearer ')).toBe(false);
    expect(stripPrefix('Bearer abc', 'Bearer ')).toBe('abc');
  });
  it('flags a shared value that repeats the prefix', () => {
    expect(headerDraftErrors({ ...EMPTY_HEADER, prefix: 'Bearer ', sharedToken: 'bearer abc' }).sharedToken?.code).toBe('prefix_already_in_value');
  });
  it('validates slug and url, and asks https for oauth', () => {
    expect(serverDraftErrors({ ...EMPTY_SERVER, slug: 'Bad Slug' }, 'none').slug).toBeDefined();
    expect(serverDraftErrors({ ...EMPTY_SERVER, slug: 'gh', url: 'http://x/mcp' }, 'oauth').url).toBeDefined();
    expect(serverDraftErrors({ ...EMPTY_SERVER, slug: 'gh', url: 'https://x/mcp' }, 'oauth')).toEqual({});
  });
  it('validates the fixed env names and duplicates for a Docker server', () => {
    const docker = { ...EMPTY_SERVER, slug: 'p', transport: 'stdio' as const, dockerImage: 'portainer/mcp:1' };
    expect(serverDraftErrors({ ...docker, env: [{ name: 'PORTAINER_URL', value: 'x' }, { name: '', value: '' }] }, 'none')).toEqual({});
    expect(serverDraftErrors({ ...docker, env: [{ name: 'bad-name', value: 'x' }] }, 'none').env).toBeDefined();
    expect(serverDraftErrors({ ...docker, env: [{ name: 'A', value: '1' }, { name: 'A', value: '2' }] }, 'none').env).toBeDefined();
  });
  it('rejects a credential variable that is malformed or collides with the fixed env', () => {
    expect(headerDraftErrors({ ...EMPTY_HEADER, credentialEnv: 'portainer' }).credentialEnv).toBeDefined();
    expect(headerDraftErrors({ ...EMPTY_HEADER, credentialEnv: 'K' }, [{ name: 'K', value: 'v' }]).credentialEnv).toBeDefined();
    expect(headerDraftErrors({ ...EMPTY_HEADER, credentialEnv: 'PORTAINER_API_KEY' }, [{ name: 'PORTAINER_URL', value: 'v' }]).credentialEnv).toBeUndefined();
    // A per-connection variable may repeat a fixed one (it becomes the default), never the token variable.
    expect(headerDraftErrors({ ...EMPTY_HEADER, credentialEnv: 'K', connectionEnv: 'PORTAINER_URL' }, [{ name: 'PORTAINER_URL', value: 'v' }]).connectionEnv).toBeUndefined();
    expect(headerDraftErrors({ ...EMPTY_HEADER, credentialEnv: 'K', connectionEnv: 'K' }).connectionEnv).toBeDefined();
  });
  it('makes a stdio server header only with a credential variable', () => {
    expect(stdioAuthKind('PORTAINER_API_KEY')).toBe('header');
    expect(stdioAuthKind('  ')).toBe('none');
  });
});
