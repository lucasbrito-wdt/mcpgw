import { beforeAll, describe, expect, it } from 'vitest';
import { setCurrentLocale } from './i18n';
import { KEY_LABEL, formatDateTime, levelLabel, maskToken, truncate } from './format';

beforeAll(() => setCurrentLocale('pt-BR'));

describe('format', () => {
  it('masks a token keeping the last 4 characters', () => {
    expect(maskToken('a3f9')).toBe('••••a3f9');
  });
  it('labels access levels in PT-BR', () => {
    expect(levelLabel('destructive')).toBe('Destrutivo');
    expect(levelLabel('read')).toBe('Leitura');
    expect(levelLabel('write')).toBe('Escrita');
    expect(levelLabel('none')).toBe('Sem acesso');
  });
  it('formats date and time in America/Sao_Paulo', () => {
    expect(formatDateTime('2026-10-30T12:00:00Z')).toBe('30/10/2026 09:00');
  });
  it('follows the active locale', () => {
    setCurrentLocale('en');
    expect(levelLabel('read')).toBe('Read');
    expect(KEY_LABEL.personal).toBe('Your key');
    expect(formatDateTime('2026-10-30T12:00:00Z')).toBe('10/30/2026 09:00');
    setCurrentLocale('pt-BR');
    expect(KEY_LABEL.personal).toBe('Sua chave');
  });
  it('truncates with an ellipsis', () => {
    expect(truncate('x'.repeat(10), 4)).toBe('xxxx…');
    expect(truncate('xy', 4)).toBe('xy');
  });
});
