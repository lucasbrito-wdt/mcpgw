import { beforeAll, describe, expect, it } from 'vitest';
import { setCurrentLocale } from '../../lib/i18n';
import type { ProbeResult, Stats, SystemInfo } from '../../lib/stats-types';
import { formatCommit, formatProbe, formatUptime, toolUsage } from './probe-format';

beforeAll(() => setCurrentLocale('pt-BR'));

const base: ProbeResult = { ok: false, latency_ms: 0, status: null, requires_auth: false, tools: null, error: null };

describe('formatProbe', () => {
  it('speaks English when the locale is en', () => {
    setCurrentLocale('en');
    try {
      expect(formatProbe({ ...base, ok: true, tools: 1, latency_ms: 5 }, 'server').text).toBe('Connection ok: 1 tool in 5 ms');
      expect(formatUptime('2026-10-31T00:00:00Z', new Date('2026-10-31T03:20:00Z'))).toBe('3 h 20 min ago');
    } finally {
      setCurrentLocale('pt-BR');
    }
  });
  it('keeps the plural for zero tools in pt-BR', () => {
    expect(formatProbe({ ...base, ok: true, tools: 0, latency_ms: 5 }, 'server').text).toBe('Conexão ok: 0 ferramentas em 5 ms');
  });
  it('reports ok with tool count and latency', () => {
    expect(formatProbe({ ...base, ok: true, tools: 12, latency_ms: 87 }, 'server')).toEqual({ tone: 'ok', text: 'Conexão ok: 12 ferramentas em 87 ms' });
  });
  it('uses the singular for one tool', () => {
    expect(formatProbe({ ...base, ok: true, tools: 1, latency_ms: 5 }, 'server').text).toBe('Conexão ok: 1 ferramenta em 5 ms');
  });
  it('explains requires_auth on a saved server', () => {
    expect(formatProbe({ ...base, requires_auth: true, status: 401 }, 'server')).toEqual({ tone: 'warn', text: 'O servidor exige autenticação (HTTP 401).' });
  });
  it('shows the backend message when a stdio server needs its credential (no HTTP status)', () => {
    const error = 'O servidor precisa de credencial: conecte um token no portal.';
    expect(formatProbe({ ...base, requires_auth: true, error }, 'server')).toEqual({ tone: 'warn', text: error });
  });
  it('treats requires_auth in the wizard as neutral-positive', () => {
    expect(formatProbe({ ...base, requires_auth: true, status: 401 }, 'wizard')).toEqual({
      tone: 'ok',
      text: 'Alcançável; exige autenticação, que você configura no próximo passo.',
    });
  });
  it('shows the error otherwise, with a fallback', () => {
    expect(formatProbe({ ...base, error: 'Sem resposta.' }, 'server')).toEqual({ tone: 'cut', text: 'Sem resposta.' });
    expect(formatProbe(base, 'wizard').tone).toBe('cut');
  });
});

const stats = (top_tools: Stats['top_tools']) => ({ top_tools }) as Stats;

describe('toolUsage', () => {
  it('matches by tool name for the server and defaults to zero', () => {
    const usage = toolUsage(
      stats([
        { server: 'gh', tool: 'list', calls: 9, denied: 2, p95_ms: 10 },
        { server: 'other', tool: 'get', calls: 5, denied: 1, p95_ms: 10 },
      ]),
      'gh',
    );
    expect(usage('list')).toEqual({ calls: 9, denied: 2 });
    expect(usage('get')).toEqual({ calls: 0, denied: 0 });
  });
  it('returns zero everywhere without stats', () => {
    expect(toolUsage(undefined, 'gh')('x')).toEqual({ calls: 0, denied: 0 });
  });
});

describe('formatCommit', () => {
  const info: SystemInfo = { version: 'v1', commit: 'abcdef123456', commit_time: null, modified: false, go_version: 'go1', public_url: '', started_at: '' };
  it('shortens to 7 chars', () => expect(formatCommit(info)).toBe('abcdef1'));
  it('flags local changes', () => expect(formatCommit({ ...info, modified: true })).toBe('abcdef1 + alterações locais'));
  it('is null without a commit', () => expect(formatCommit({ ...info, commit: null })).toBeNull());
});

describe('formatUptime', () => {
  const start = '2026-10-31T00:00:00Z';
  const at = (ms: number) => new Date(Date.parse(start) + ms);
  it('formats minutes, hours and days', () => {
    expect(formatUptime(start, at(30_000))).toBe('há menos de 1 min');
    expect(formatUptime(start, at(5 * 60_000))).toBe('há 5 min');
    expect(formatUptime(start, at(3 * 3_600_000 + 20 * 60_000))).toBe('há 3 h 20 min');
    expect(formatUptime(start, at(2 * 86_400_000 + 4 * 3_600_000))).toBe('há 2 d 4 h');
  });
});
