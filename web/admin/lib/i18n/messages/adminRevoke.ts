import { defineMessages } from '../core';

/** The revoke dialog shell shared by connections, consents and grants. */
export const adminRevoke = defineMessages({
  en: {
    revoke: 'Revoke',
    cancel: 'Cancel',
    retry: 'Try again',
    reasonLabel: 'Reason (the user will see this text)',
    reasonPlaceholder: 'E.g. key pasted in a public chat',
    hintReady: 'The action is recorded in the audit log with the reason and your user.',
    hintMissing: 'Write a reason to enable the button. The action is recorded in the audit log.',
  },
  'pt-BR': {
    revoke: 'Revogar',
    cancel: 'Cancelar',
    retry: 'Tentar de novo',
    reasonLabel: 'Motivo (o usuário verá este texto)',
    reasonPlaceholder: 'Ex.: chave colada num chat público',
    hintReady: 'A ação fica no audit log com o motivo e o seu usuário.',
    hintMissing: 'Escreva um motivo para liberar o botão. A ação fica no audit log.',
  },
});
