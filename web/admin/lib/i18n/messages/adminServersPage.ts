import { defineMessages } from '../core';

export const adminServersPage = defineMessages({
  en: {
    pageTitle: 'Servers (admin)',
    title: 'Servers',
    description: 'The MCP servers the gateway exposes. Each one declares only what its upstream accepts: no credential, OAuth or a header.',
    add: 'Add server',
    loading: 'Loading servers',
  },
  'pt-BR': {
    pageTitle: 'Servidores (admin)',
    title: 'Servidores',
    description: 'Os servidores MCP que o gateway expõe. Cada um declara só o que o upstream aceita: nenhuma credencial, OAuth ou um header.',
    add: 'Adicionar servidor',
    loading: 'Carregando servidores',
  },
});
