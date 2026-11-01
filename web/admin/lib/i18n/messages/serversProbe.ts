import { defineMessages } from '../core';

/** Probe results ("Test URL", "Test connection") and the commit and uptime lines of the settings page. */
export const serversProbe = defineMessages({
  en: {
    ok_one: 'Connection ok: {n} tool in {ms} ms',
    ok_other: 'Connection ok: {n} tools in {ms} ms',
    reachable: 'Reachable; it requires authentication, which you set up in the next step.',
    requiresAuth: 'The server requires authentication (HTTP {status}).',
    failed: "Couldn't connect to the server.",
    commitModified: '{short} + local changes',
    uptimeLessThanMinute: 'less than 1 min ago',
    uptimeMinutes: '{m} min ago',
    uptimeHours: '{h} h {m} min ago',
    uptimeDays: '{d} d {h} h ago',
  },
  'pt-BR': {
    ok_one: 'Conexão ok: {n} ferramenta em {ms} ms',
    ok_other: 'Conexão ok: {n} ferramentas em {ms} ms',
    reachable: 'Alcançável; exige autenticação, que você configura no próximo passo.',
    requiresAuth: 'O servidor exige autenticação (HTTP {status}).',
    failed: 'Não foi possível conectar ao servidor.',
    commitModified: '{short} + alterações locais',
    uptimeLessThanMinute: 'há menos de 1 min',
    uptimeMinutes: 'há {m} min',
    uptimeHours: 'há {h} h {m} min',
    uptimeDays: 'há {d} d {h} h',
  },
});
