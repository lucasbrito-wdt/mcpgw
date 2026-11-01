import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import { countLabel } from '../../hooks/countLabel';
import { I18nProvider, makeTranslator } from '../../lib/i18n';
import { portalAgents } from '../../lib/i18n/messages/portalAgents';
import { portalServers } from '../../lib/i18n/messages/portalServers';
import { clientSetups, missingMethodNote } from '../portal-v5/clients';
import { Pill } from './badge';

const render = (locale: 'en' | 'pt-BR', kind: 'personal' | 'destroy') =>
  renderToStaticMarkup(createElement(I18nProvider, { locale }, createElement(Pill, { kind })));

describe('Pill', () => {
  it('keeps the pt-BR copy and has an English version', () => {
    expect(render('pt-BR', 'personal')).toContain('Sua chave');
    expect(render('pt-BR', 'destroy')).toContain('Destrutivo');
    expect(render('en', 'personal')).toContain('Your key');
  });
});

describe('countLabel', () => {
  it('treats only 1 as singular, so 0 stays plural in pt-BR', () => {
    const t = makeTranslator('pt-BR', portalServers);
    expect(countLabel(t, 'selected', 1)).toBe('1 selecionada');
    expect(countLabel(t, 'selected', 0)).toBe('0 selecionadas');
    const en = makeTranslator('en', portalServers);
    expect(countLabel(en, 'serverCount', 2)).toBe('2 servers');
  });
});

describe('client setup', () => {
  it('translates the prose and keeps commands and config as they are', () => {
    const pt = makeTranslator('pt-BR', portalAgents);
    const en = makeTranslator('en', portalAgents);
    const url = 'https://gw.example/mcp';
    expect(clientSetups(url, pt)['claude-code'].file.path).toBe('.mcp.json, na raiz do projeto');
    expect(clientSetups(url, en)['claude-code'].file.path).toBe('.mcp.json, at the project root');
    expect(clientSetups(url, en)['claude-code'].cli).toBe(`claude mcp add --transport http mcpgw ${url}`);
    expect(missingMethodNote(clientSetups(url, pt).codex, 'oneclick', 'Codex', pt)).toBe('O Codex não tem link de instalação. Use o comando.');
  });
});
