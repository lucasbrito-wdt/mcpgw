import { defineMessages } from '../core';

export const login = defineMessages({
  en: {
    adminTitle: 'Sign in to the admin',
    adminSubtitle: 'Admin users only.',
    portalTitle: 'Sign in to the portal',
    portalSubtitle: 'Use the same email and password as the gateway.',
    email: 'Email',
    emailPlaceholder: 'you@company.com',
    password: 'Password',
    submit: 'Sign in',
    areaNav: 'Sign-in area',
    areaAdmin: 'Admin',
    areaPortal: 'Portal',
    invalidCredentials: 'Wrong email or password.',
    tooManyAttempts: 'Too many attempts. Wait a few minutes and try again.',
    brandDescription:
      "One MCP address for your agents and the servers behind it. On every call, mcpgw uses your own key when you have one; otherwise the team's shared key.",
    serversCaption: 'Servers',
  },
  'pt-BR': {
    adminTitle: 'Entrar no painel',
    adminSubtitle: 'Só usuários com permissão de admin.',
    portalTitle: 'Entrar no portal',
    portalSubtitle: 'Use o mesmo e-mail e senha do gateway.',
    email: 'E-mail',
    emailPlaceholder: 'voce@empresa.com',
    password: 'Senha',
    submit: 'Entrar',
    areaNav: 'Área de login',
    areaAdmin: 'Admin',
    areaPortal: 'Portal',
    invalidCredentials: 'E-mail ou senha incorretos.',
    tooManyAttempts: 'Muitas tentativas. Aguarde alguns minutos e tente de novo.',
    brandDescription:
      'Um endereço MCP para os seus agentes e os servidores atrás dele. Em cada chamada o mcpgw usa a sua chave quando você tem uma; senão, a compartilhada do time.',
    serversCaption: 'Servidores',
  },
});
