import { beforeAll, describe, expect, it } from 'vitest';
import { setCurrentLocale } from './i18n';
import { stdioEnvError, stdioEnvSetBody, stdioEnvUnsetBody } from './stdio-env';

beforeAll(() => setCurrentLocale('pt-BR'));

describe('stdioEnvError', () => {
  it('accepts a valid name and value', () => {
    expect(stdioEnvError(' PORTAINER_TIMEOUT ', '120', 'PORTAINER_API_KEY')).toEqual({});
  });
  it('rejects bad names and the credential variable', () => {
    expect(stdioEnvError('lower', 'v', '').name).toBeDefined();
    expect(stdioEnvError('1A', 'v', '').name).toBeDefined();
    expect(stdioEnvError('PORTAINER_API_KEY', 'v', 'PORTAINER_API_KEY').name).toContain('token');
  });
  it('rejects empty, multiline and oversized values', () => {
    expect(stdioEnvError('A', '  ', '').value).toBeDefined();
    expect(stdioEnvError('A', 'a\nb', '').value).toBeDefined();
    expect(stdioEnvError('A', 'x'.repeat(4097), '').value).toBeDefined();
    expect(stdioEnvError('A', 'x'.repeat(4096), '').value).toBeUndefined();
  });
});

describe('stdio env bodies', () => {
  it('builds merge payloads', () => {
    expect(stdioEnvSetBody(' A ', 'v')).toEqual({ stdio_env: { set: { A: 'v' } } });
    expect(stdioEnvUnsetBody('A')).toEqual({ stdio_env: { unset: ['A'] } });
  });
});
