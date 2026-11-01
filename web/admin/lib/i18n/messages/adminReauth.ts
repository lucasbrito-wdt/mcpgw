import { defineMessages } from '../core';

export const adminReauth = defineMessages({
  en: {
    title: 'Confirm your password to continue',
    description: 'This sensitive action needs your password again. The confirmation is valid for 5 minutes.',
    password: 'Password',
    cancel: 'Cancel',
    confirm: 'Confirm',
    required: 'Enter your password.',
    wrong: 'Wrong password. Check it and try again.',
  },
  'pt-BR': {
    title: 'Confirme sua senha para continuar',
    description: 'Esta ação sensível exige a senha de novo. A confirmação vale por 5 minutos.',
    password: 'Senha',
    cancel: 'Cancelar',
    confirm: 'Confirmar',
    required: 'Informe a sua senha.',
    wrong: 'Senha incorreta. Confira e tente de novo.',
  },
});
