import { defineMessages } from '../core';

/** Signature visuals: the route hero of the server detail and the switchboard captions. Product and server names stay as they are. */
export const signature = defineMessages({
  en: {
    routeLabel: 'Route of one of your calls',
    you: 'You',
    picksKey: 'picks the key',
    yourKey: 'Your key',
    personalFallback: 'the account you connect',
    sharedFallback: 'team key',
    openSub: 'the server needs no key',
    noneSub: 'nobody has connected yet',
    sharedReserve: 'the shared one stays as backup',
    keyIdle: 'Key',
    agentsCaption: 'Your agents',
    serversCaption_one: '{n} server',
    serversCaption_other: '{n} servers',
  },
  'pt-BR': {
    routeLabel: 'Rota de uma chamada sua',
    you: 'Você',
    picksKey: 'escolhe a chave',
    yourKey: 'Sua chave',
    personalFallback: 'a conta que você conectar',
    sharedFallback: 'chave do time',
    openSub: 'o servidor não pede chave',
    noneSub: 'ninguém conectou ainda',
    sharedReserve: 'a compartilhada fica de reserva',
    keyIdle: 'Chave',
    agentsCaption: 'Seus agentes',
    serversCaption_one: '{n} servidores',
    serversCaption_other: '{n} servidores',
  },
});
