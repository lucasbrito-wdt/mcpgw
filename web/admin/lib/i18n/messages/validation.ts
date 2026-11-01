import { defineMessages } from '../core';

export const validation = defineMessages({
  en: {
    envRequired: 'Enter {name}: the server has no default value.',
    valueLimit: 'Use up to {max} characters, with no line breaks.',
    envNamesNeedCredential: "Per-connection variables require each user's credential.",
    envNamesMax: 'At most {max} variables.',
    envNameInvalid: 'Invalid name: {name}. Use A-Z, 0-9 and _, up to 64 characters, not starting with a digit.',
    envNameDuplicate: 'The variable {name} is repeated.',
    envNameIsToken: '{name} is already the token variable.',
    stdioNameFormat: 'Use A-Z, 0-9 and _, up to 64 characters, not starting with a digit.',
    stdioNameIsToken: "{name} is each user's token variable.",
    stdioValueRequired: 'Enter a value.',
  },
  'pt-BR': {
    envRequired: 'Informe {name}: o servidor não tem valor padrão.',
    valueLimit: 'Use até {max} caracteres, sem quebras de linha.',
    envNamesNeedCredential: 'Variáveis por conexão exigem a credencial de cada usuário.',
    envNamesMax: 'No máximo {max} variáveis.',
    envNameInvalid: 'Nome inválido: {name}. Use A-Z, 0-9 e _, até 64, sem começar por número.',
    envNameDuplicate: 'A variável {name} está repetida.',
    envNameIsToken: '{name} já é a variável do token.',
    stdioNameFormat: 'Use A-Z, 0-9 e _, até 64, sem começar por número.',
    stdioNameIsToken: '{name} é a variável do token de cada usuário.',
    stdioValueRequired: 'Informe o valor.',
  },
});
