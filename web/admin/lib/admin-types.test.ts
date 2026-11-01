import { beforeAll, describe, expect, it } from 'vitest';
import { setCurrentLocale } from './i18n';
import { buildAddServerBody, serverStatusLabel, credentialSummary, formatDefinition } from './admin-types';

beforeAll(() => setCurrentLocale('pt-BR'));

describe('buildAddServerBody', () => {
  it('drops header fields unless auth_kind is header', () => {
    expect(buildAddServerBody({ slug: 'gh', url: 'https://x/mcp', auth_kind: 'none', transport: 'auto', header_name: 'Authorization' }))
      .toEqual({ slug: 'gh', url: 'https://x/mcp', auth_kind: 'none', transport: 'auto' });
  });
  it('keeps header config and category for header servers', () => {
    expect(buildAddServerBody({ slug: 'gh', url: 'https://x/mcp', auth_kind: 'header', transport: 'auto', category: 'dev',
      header_name: 'Authorization', header_prefix: 'Bearer ', header_format_hint: 'github_pat_', header_help_url: 'https://github.com/settings/tokens' }))
      .toMatchObject({ auth_kind: 'header', category: 'dev', header: { name: 'Authorization', prefix: 'Bearer ', format_hint: 'github_pat_' } });
  });
  it('never emits launcher host', () => {
    const body = buildAddServerBody({ slug: 'fs', transport: 'stdio', docker_image: 'mcp/fs:1', auth_kind: 'none' });
    expect(body.stdio_launcher).toBe('docker');
  });
  it('sends a per-user credential stdio server as header with only the portal hints', () => {
    const body = buildAddServerBody({
      slug: 'portainer', transport: 'stdio', docker_image: 'portainer/mcp:1', stdio_command: ' run ', stdio_network: true,
      stdio_env: [{ name: 'PORTAINER_URL', value: 'https://p' }, { name: ' ', value: '' }], credential_env: ' PORTAINER_API_KEY ',
      auth_kind: 'oauth', header_name: 'Authorization', header_prefix: 'Bearer ', header_help_url: 'https://p/help',
    });
    expect(body).toEqual({
      slug: 'portainer', transport: 'stdio', auth_kind: 'header', stdio_launcher: 'docker', stdio_image: 'portainer/mcp:1', stdio_command: 'run',
      stdio_network: true, stdio_env: [{ name: 'PORTAINER_URL', value: 'https://p' }], credential_env: 'PORTAINER_API_KEY', header: { help_url: 'https://p/help' },
    });
  });
  it('sends the per-connection variables only with a per-user credential', () => {
    const form = { slug: 'p', transport: 'stdio', docker_image: 'p/mcp:1', auth_kind: 'header', connection_env: ['PORTAINER_URL'] } as const;
    expect(buildAddServerBody({ ...form, connection_env: [...form.connection_env], credential_env: 'KEY' }).connection_env).toEqual(['PORTAINER_URL']);
    expect(buildAddServerBody({ ...form, connection_env: [...form.connection_env] }).connection_env).toBeUndefined();
  });
  it('sends a stdio server without credential as none, with no header and network off by default', () => {
    expect(buildAddServerBody({ slug: 'fs', transport: 'stdio', docker_image: 'mcp/fs:1', auth_kind: 'header', header_name: 'X' }))
      .toEqual({ slug: 'fs', transport: 'stdio', auth_kind: 'none', stdio_launcher: 'docker', stdio_image: 'mcp/fs:1', stdio_network: false });
  });
});
it('labels', () => {
  expect(serverStatusLabel('disabled')).toBe('Desativado');
  expect(credentialSummary({ auth_kind: 'header', has_shared_connection: false })).toBe('Sem conexão compartilhada');
  expect(credentialSummary({ auth_kind: 'header', has_shared_connection: true })).toBe('Conexão compartilhada ativa');
});
it('formats definition parts as text', () => {
  expect(formatDefinition(null)).toBe('');
  expect(formatDefinition('plain')).toBe('plain');
  expect(formatDefinition({ a: 1 })).toBe('{\n  "a": 1\n}');
});
