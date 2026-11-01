import { defineMessages } from '../core';

/** The Access page and its ceiling matrix. */
export const adminAccess = defineMessages({
  en: {
    pageTitle: 'Access (admin)',
    title: 'Access',
    description:
      'Each person’s ceiling on each server: No access, Read, Write or Destructive. A tool only runs if its level fits under the ceiling. Changes apply immediately and are recorded in the admin log.',
    searchLabel: 'Search people',
    searchPlaceholder: 'Email',
    loading: 'Loading access ceilings',
    noUsersTitle: 'No users yet',
    noUsersBody: 'Create the first one on the Users page to set access ceilings.',
    noServersTitle: 'No servers yet',
    noServersBody: 'Add a server to set who can use it.',
    noMatchTitle: 'No one matches "{query}"',
    noMatchBody: 'Search looks at the email.',
    matrixCaption: 'Each person’s access ceiling on each server',
    person: 'Person',
    ceilingLabel: 'Ceiling of {email} on {slug}',
    saveError: 'Could not save the ceiling. Try again.',
  },
  'pt-BR': {
    pageTitle: 'Acesso (admin)',
    title: 'Acesso',
    description:
      'O teto de cada pessoa em cada servidor: Sem acesso, Leitura, Escrita ou Destrutivo. Uma tool só roda se o nível dela couber no teto. A mudança vale na hora e fica no log admin.',
    searchLabel: 'Buscar pessoas',
    searchPlaceholder: 'E-mail',
    loading: 'Carregando tetos de acesso',
    noUsersTitle: 'Nenhum usuário ainda',
    noUsersBody: 'Crie o primeiro na página Usuários para definir tetos de acesso.',
    noServersTitle: 'Nenhum servidor ainda',
    noServersBody: 'Adicione um servidor para definir quem pode usá-lo.',
    noMatchTitle: 'Nenhuma pessoa para "{query}"',
    noMatchBody: 'A busca olha o e-mail.',
    matrixCaption: 'Teto de acesso de cada pessoa em cada servidor',
    person: 'Pessoa',
    ceilingLabel: 'Teto de {email} em {slug}',
    saveError: 'Não foi possível salvar o teto. Tente de novo.',
  },
});
