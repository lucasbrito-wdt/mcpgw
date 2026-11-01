<p align="center">
  <img src="web/admin/brand/icon.svg" width="96" height="96" alt="Logo do mcpgw">
</p>

<h1 align="center">mcpgw</h1>

<p align="center"><strong>Um endereço MCP para os seus agentes.</strong></p>

<p align="center">
  <a href="https://github.com/lucasbrito-wdt/mcpgw/actions/workflows/ci.yml"><img src="https://github.com/lucasbrito-wdt/mcpgw/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="Licença: Apache-2.0"></a>
</p>

<p align="center"><a href="README.md">English</a> | Português (Brasil)</p>

> Esta é a versão em português do [README.md](README.md). Em caso de divergência, o README em inglês
> é a referência. O documento [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) está apenas em inglês.

O mcpgw é um gateway self-hosted para o [Model Context Protocol](https://modelcontextprotocol.io),
escrito em Go. Seus agentes se conectam a uma única URL. Atrás dela você anexa quantos servidores
MCP quiser, decide quem pode chamar o quê e mantém todas as credenciais no lado do servidor. Ele é
distribuído como um único binário e precisa apenas do PostgreSQL.

Em vez de carregar o schema de cada tool no contexto do agente, o mcpgw expõe duas tools, `search` e
`execute`. O agente busca o que precisa e depois escreve uma pequena função JavaScript que chama
`tools.<server>.<tool>()` dentro de um sandbox.

## Por que mcpgw

- **Contexto pequeno, muitos servidores.** O agente enxerga duas tools, não centenas de schemas, e
  encadeia chamadas em código em vez de passar pelo modelo a cada etapa.
  Veja [code mode](docs/ARCHITECTURE.md#code-mode-instead-of-tool-schemas) e
  [o sandbox](docs/ARCHITECTURE.md#the-javascript-sandbox).
- **A chave certa em cada chamada.** A chave pessoal do usuário é usada quando ele tem uma; caso
  contrário, a chave compartilhada do time. Uma mesma pessoa pode manter várias contas nomeadas por
  servidor. Veja [credential resolution](docs/ARCHITECTURE.md#credential-resolution) e
  [connection profiles](docs/ARCHITECTURE.md#connection-profiles).
- **Menor privilégio por usuário e por agente.** O ceiling (teto) definido pelo admin e o consent
  (consentimento) do próprio usuário precisam, os dois, permitir a tool. Veja
  [policy ceilings and consent](docs/ARCHITECTURE.md#policy-ceilings-and-consent).
- **As definições das tools não mudam por baixo de você.** Cada tool é fixada por um hash, e uma
  definição alterada fica retida até que um admin a revise. Veja
  [catalog trust](docs/ARCHITECTURE.md#catalog-trust).
- **Segredos continuam secretos, e tudo fica registrado.** As credenciais são cifradas no Postgres e
  nunca entram no sandbox, e cada execução e cada chamada interna é auditada. Veja
  [credentials at rest](docs/ARCHITECTURE.md#credentials-at-rest) e
  [audit](docs/ARCHITECTURE.md#audit).
- **Um binário, um banco de dados.** A interface de admin e do portal vai embutida, e mudanças feitas
  em qualquer lugar chegam a todos os gateways em execução sem reiniciar. Veja
  [a interface embutida](docs/ARCHITECTURE.md#embedded-ui-and-its-csp) e
  [hot reload](docs/ARCHITECTURE.md#hot-reload-with-listen-and-notify).

## Funcionalidades

- Um único endpoint MCP, `/mcp`: Streamable HTTP, stateless, protocolo MCP na versão `2026-07-28`.
- Code mode: `search` devolve declarações TypeScript das tools que o usuário pode chamar, e `execute`
  roda o JavaScript do agente em QuickJS compilado para WebAssembly (sem cgo).
- Transportes de upstream: Streamable HTTP, SSE legado e stdio. Servidores stdio rodam por padrão
  em um container Docker descartável e endurecido.
- Autenticação de upstream: nenhuma, OAuth (discovery, PKCE, resource indicators, client metadata
  documents ou dynamic registration), um token de header colado pelo usuário, ou uma variável de
  ambiente por usuário para servidores stdio.
- Conexões pessoais e compartilhadas, e connection profiles nomeados escolhidos a cada chamada.
- Authorization server OAuth 2.1 para clientes MCP, com client metadata documents e dynamic client
  registration, PKCE, access tokens ES256 e refresh tokens com rotação.
- Níveis de confiança (`read`, `write`, `destructive`), ceilings por usuário, consent por agente e
  tool pinning com revisão.
- Trilha de auditoria das execuções e das chamadas internas de tools, além de um log das ações de
  admin.
- Credenciais cifradas em repouso com AES-256-GCM.
- Hot reload via `LISTEN`/`NOTIFY` do Postgres.
- Web admin e portal do usuário, e uma CLI de admin.
- `GET /healthz`, e pprof opcional em um endereço de loopback.

O mcpgw faz proxy de tools. Resources e prompts dos upstreams não são expostos.

## Instalação

Os instaladores abaixo configuram uma release já compilada como serviço do sistema. Para compilar a
partir do código-fonte, siga o [Quickstart](#quickstart).

As releases são publicadas no [GitHub Releases](https://github.com/lucasbrito-wdt/mcpgw/releases). Cada
instalador baixa o arquivo da sua plataforma, confere o hash SHA-256 contra o `SHA256SUMS` e para se o
hash não bater.

### Pré-requisitos

- PostgreSQL 18 ou mais recente. O instalador pergunta se você quer usar um banco que já possui ou
  instalar o PostgreSQL 18 nesta máquina.
- Linux com systemd, macOS ou Windows de 64 bits. No Linux sem systemd, adicione `--no-service`.
- Privilégios de administrador: `sudo` no Linux e no macOS, um PowerShell elevado no Windows.

### Linux e macOS

```bash
curl -fsSL https://github.com/lucasbrito-wdt/mcpgw/releases/latest/download/install.sh | sudo sh
```

As opções vão depois de `sh -s --`:

```bash
curl -fsSL https://github.com/lucasbrito-wdt/mcpgw/releases/latest/download/install.sh \
  | sudo sh -s -- --database-url 'postgres://mcpgw:secret@db.internal:5432/mcpgw' \
      --admin-email admin@example.com
```

Sem terminal interativo, defina as variáveis no `env`, porque o `sudo` limpa o ambiente:

```bash
curl -fsSL https://github.com/lucasbrito-wdt/mcpgw/releases/latest/download/install.sh \
  | sudo env MCPGW_INSTALL_NON_INTERACTIVE=1 MCPGW_INSTALL_INSTALL_POSTGRES=1 sh
```

| Opção | Variável de ambiente | Descrição |
| --- | --- | --- |
| `--version V` | `MCPGW_INSTALL_VERSION` | Release a instalar, com ou sem o `v`. Padrão: `latest`. Obrigatória com um `--base-url` personalizado. |
| `--base-url URL\|DIR` | `MCPGW_INSTALL_BASE_URL` | Raiz de download ou diretório local. Os arquivos são lidos de `<base>/v<version>/<arquivo>`. |
| `--database-url URL` | `MCPGW_INSTALL_DATABASE_URL` | Banco PostgreSQL 18+ já existente. Exclusiva com `--install-postgres`. |
| `--install-postgres` | `MCPGW_INSTALL_INSTALL_POSTGRES=1` | Instala o PostgreSQL 18 nesta máquina e cria o role e o banco `mcpgw`. |
| `--public-url URL` | `MCPGW_INSTALL_PUBLIC_URL` | URL externa do gateway. Padrão: `http://<listen-addr>`. |
| `--listen-addr ADDR` | `MCPGW_INSTALL_LISTEN_ADDR` | Endereço em que o gateway escuta. Padrão: `127.0.0.1:8787`. |
| `--engine qjs\|monty` | `MCPGW_INSTALL_ENGINE` | Engine do sandbox. Padrão: `qjs`. |
| `--admin-email E` | `MCPGW_INSTALL_ADMIN_EMAIL` | Cria o primeiro usuário admin. |
| `--admin-password P` | `MCPGW_INSTALL_ADMIN_PASSWORD` | Senha desse admin. Prefira a variável ou o prompt: os argumentos ficam visíveis no `ps`. |
| `--non-interactive` | `MCPGW_INSTALL_NON_INTERACTIVE=1` | Nunca pergunta nada. Falha se faltar algum valor obrigatório. |
| `--no-service` | `MCPGW_INSTALL_NO_SERVICE=1` | Instala apenas os binários e a configuração. Você sobe o gateway manualmente. |
| `--uninstall` | nenhuma | Para e remove o serviço e os binários. |
| `--purge` | nenhuma | Com `--uninstall`: remove também a configuração, os logs e o usuário do serviço. |
| `-h`, `--help` | nenhuma | Mostra o uso. |

Sem `--database-url` nem `--install-postgres`, uma execução interativa pergunta qual usar. Uma execução
não interativa falha.

O `--install-postgres` usa o repositório apt do PGDG no Debian e no Ubuntu, o repositório dnf do PGDG no
RHEL e no Fedora, e o Homebrew (`postgresql@18`) no macOS. Em outras distribuições, use
`--database-url`. No macOS, rode o instalador com `sudo` a partir do seu usuário normal, não de um shell
root, porque o Homebrew se recusa a rodar como root.

### Windows

Abra o PowerShell como Administrador e rode:

```powershell
irm https://github.com/lucasbrito-wdt/mcpgw/releases/latest/download/install.ps1 | iex
```

Quando o script é executado via pipe, ele não recebe parâmetros. Defina antes as variáveis `MCPGW_INSTALL_*`:

```powershell
$env:MCPGW_INSTALL_DATABASE_URL = 'postgres://mcpgw:secret@db.internal:5432/mcpgw'
irm https://github.com/lucasbrito-wdt/mcpgw/releases/latest/download/install.ps1 | iex
```

Para passar parâmetros, baixe o script e rode a partir de um shell elevado:

```powershell
Invoke-WebRequest https://github.com/lucasbrito-wdt/mcpgw/releases/latest/download/install.ps1 -OutFile install.ps1
powershell -ExecutionPolicy Bypass -File .\install.ps1 -InstallPostgres `
  -AdminEmail admin@example.com -AdminPassword (Read-Host 'Admin password' -AsSecureString)
```

| Parâmetro | Variável de ambiente | Descrição |
| --- | --- | --- |
| `-Version` | `MCPGW_INSTALL_VERSION` | Release a instalar. Padrão: a mais recente. |
| `-BaseUrl` | `MCPGW_INSTALL_BASE_URL` | Raiz de download ou diretório local. |
| `-DatabaseUrl` | `MCPGW_INSTALL_DATABASE_URL` | Banco PostgreSQL 18+ já existente. |
| `-InstallPostgres` | `MCPGW_INSTALL_INSTALL_POSTGRES=1` | Instala o PostgreSQL 18 com o winget (o instalador da EDB é o fallback). |
| `-PublicUrl` | `MCPGW_INSTALL_PUBLIC_URL` | URL externa do gateway. |
| `-ListenAddr` | `MCPGW_INSTALL_LISTEN_ADDR` | Padrão: `127.0.0.1:8787`. |
| `-Engine` | `MCPGW_INSTALL_ENGINE` | `qjs` (padrão) ou `monty`. |
| `-AdminEmail` | `MCPGW_INSTALL_ADMIN_EMAIL` | Cria o primeiro usuário admin. Precisa de senha. |
| `-AdminPassword` | `MCPGW_INSTALL_ADMIN_PASSWORD` | Um `SecureString` na linha de comando. A variável guarda texto puro. |
| `-NonInteractive` | `MCPGW_INSTALL_NON_INTERACTIVE=1` | Nunca pergunta nada. |
| `-NoService` | `MCPGW_INSTALL_NO_SERVICE=1` | Instala apenas os arquivos e a configuração. |
| `-Uninstall` | `MCPGW_INSTALL_UNINSTALL=1` | Remove o serviço, os binários e a entrada no PATH. |
| `-Purge` | `MCPGW_INSTALL_PURGE=1` | Com `-Uninstall`: remove também o `%ProgramData%\mcpgw`. |

O script roda no Windows PowerShell 5.1 e no PowerShell 7. As releases para Windows são somente amd64.

### Onde ficam os arquivos

| | Linux | macOS | Windows |
| --- | --- | --- | --- |
| Binários | `/opt/mcpgw/bin/` (`mcpgw`, `montyrs-worker`), com link em `/usr/local/bin/mcpgw` | `/usr/local/mcpgw/bin/`, com link em `/usr/local/bin/mcpgw` | `C:\Program Files\mcpgw\`, adicionado ao `PATH` da máquina |
| Configuração | `/etc/mcpgw/mcpgw.env` | `/Library/Application Support/mcpgw/mcpgw.env` | `%ProgramData%\mcpgw\mcpgw.env` |
| Serviço | unit do systemd `mcpgw` | daemon do launchd `io.github.lucasbrito-wdt.mcpgw` | serviço do Windows `mcpgw` |
| Usuário do serviço | `mcpgw` | `_mcpgw` | `NT SERVICE\mcpgw` |
| Logs | `journalctl -u mcpgw -f` | `/Library/Logs/mcpgw/mcpgw.log` | `%ProgramData%\mcpgw\logs\mcpgw.log` |

O instalador do Windows guarda a senha de superusuário do PostgreSQL que ele gerou em
`%ProgramData%\mcpgw\pg-superuser.txt`, legível apenas por Administradores e SYSTEM.

### Atualização

Rode o mesmo comando de novo. O instalador substitui os binários, roda as migrations e reinicia o
serviço. A configuração existente é mantida.

Como a configuração é mantida, `--database-url`, `--listen-addr`, `--public-url` e `--engine` só valem
na primeira instalação. Para mudá-los depois, edite o arquivo de ambiente e reinicie o serviço:

```bash
sudo systemctl restart mcpgw                                      # Linux
sudo launchctl kickstart -k system/io.github.lucasbrito-wdt.mcpgw  # macOS
Restart-Service mcpgw                                             # Windows, elevado
```

### Desinstalação

```bash
curl -fsSL https://github.com/lucasbrito-wdt/mcpgw/releases/latest/download/install.sh | sudo sh -s -- --uninstall
curl -fsSL https://github.com/lucasbrito-wdt/mcpgw/releases/latest/download/install.sh | sudo sh -s -- --uninstall --purge
```

No Windows, rode `.\install.ps1 -Uninstall`, acrescentando `-Purge` para remover a configuração.

A desinstalação mantém a configuração e os logs, e nunca mexe no banco de dados. O `--purge` remove
também o usuário do serviço. Para apagar o banco, rode o comando abaixo como superusuário do PostgreSQL.
A ação é irreversível:

```sql
DROP DATABASE mcpgw;
DROP ROLE mcpgw;
```

> **Faça backup da configuração.** O arquivo de ambiente guarda o `MCPGW_MASTER_KEY`, que o instalador
> gera na primeira instalação. Todos os segredos gravados no banco são cifrados com essa chave. Copie o
> arquivo de ambiente para um lugar seguro. Se você perder a chave ou remover a configuração com
> `--purge`, os dados existentes no banco não poderão mais ser decifrados. Uma nova instalação precisará
> então de um banco vazio.

### Releases

Cada tag `v*` publica uma GitHub Release com um arquivo por plataforma, chamado
`mcpgw_<version>_<os>_<arch>.tar.gz` (`.zip` no Windows), e um arquivo `SHA256SUMS`. Os dois instaladores
são anexados à release. Releases com hífen na versão são marcadas como prerelease.

Os dois instaladores conferem o SHA-256 do arquivo contra o `SHA256SUMS` antes de extraí-lo. O
`--base-url` (`-BaseUrl`) aceita um espelho ou um diretório local com o mesmo layout,
`<base>/v<version>/<arquivo>`.

## Quickstart

### Requisitos

- Go 1.26 ou mais recente (veja o `go.mod`).
- PostgreSQL 18 ou mais recente. As migrations usam o `uuidv7()` nativo.
- Node.js 22 e pnpm 9, apenas para compilar a interface web. Sem eles, `go build ./cmd/mcpgw` ainda
  funciona, mas `/admin/` e `/portal/` respondem 503 com uma página provisória.
- Docker, apenas se você quiser servidores MCP stdio em containers.

### 1. Configure

```bash
git clone https://github.com/lucasbrito-wdt/mcpgw.git
cd mcpgw
cp .env.example .env
chmod 600 .env
```

Crie um banco de dados vazio (por exemplo `createdb mcp_gateway`) e edite o `.env`. Defina
`MCPGW_DATABASE_URL` e gere a master key que cifra todos os segredos:

```bash
openssl rand -base64 32   # cole a saída como MCPGW_MASTER_KEY
```

O binário lê o `.env` do diretório atual. Variáveis já definidas no ambiente têm prioridade. Faça
backup da master key: os valores cifrados não podem ser abertos sem ela.

### 2. Compile e rode as migrations

```bash
make build                # compila a interface web e depois o bin/mcpgw
bin/mcpgw migrate         # o serve também aplica as migrations pendentes ao iniciar
```

### 3. Crie o primeiro admin

A senha deve ter de 12 a 1024 caracteres. Ela é lida de `MCPGW_ADMIN_PASSWORD` ou da primeira linha
do stdin, nunca do argv.

```bash
export MCPGW_ADMIN_PASSWORD='uma-senha-longa-a-sua-escolha'
bin/mcpgw admin user create --email admin@example.com --admin
```

### 4. Suba o gateway

```bash
bin/mcpgw serve
curl http://127.0.0.1:8787/healthz   # imprime "ok" quando o banco está acessível
```

### 5. Dê algo para os agentes chamarem

Um gateway novo não tem servidores, e admins não têm acesso implícito. Adicione um servidor
upstream, sincronize o catálogo e conceda a si mesmo um ceiling nele. A primeira sincronização de um
servidor define a baseline confiável das tools dele.

```bash
bin/mcpgw admin server add --slug acme --url https://mcp.example.com/mcp --auth none
bin/mcpgw admin catalog sync --slug acme
bin/mcpgw admin ceiling set --email admin@example.com --slug acme --level write
```

Você pode fazer o mesmo no web admin. Outros tipos de servidor:

```bash
# Upstream OAuth: os usuários conectam a conta no portal, depois um admin sincroniza como esse usuário
bin/mcpgw admin server add --slug globex --url https://mcp.example.com/globex --auth oauth
bin/mcpgw admin catalog sync --slug globex --as admin@example.com

# Servidor stdio em um container endurecido, sem acesso à rede
bin/mcpgw admin server add --slug files --transport stdio \
  --docker-image ghcr.io/acme/files-mcp:1.0 --auth none
```

### 6. Conecte os seus agentes

A URL do gateway é a sua URL pública mais `/mcp`: `http://localhost:8787/mcp` em uma execução local.

```bash
# Claude Code
claude mcp add --transport http mcpgw http://localhost:8787/mcp

# Codex
codex mcp add mcpgw --url http://localhost:8787/mcp
codex mcp login mcpgw
```

No Claude Code, autentique pelo menu `/mcp`. No Cursor, abra Settings, MCP, e adicione um novo
servidor com a URL, ou coloque `{"mcpServers": {"mcpgw": {"url": "http://localhost:8787/mcp"}}}` em
`~/.cursor/mcp.json`. O portal também mostra essas instruções em `/portal/agents/`.

Na primeira conexão, o cliente abre a página de login do gateway. Depois de entrar, você escolhe um
nível de acesso para cada servidor, até o seu ceiling. O agente passa então a enxergar `search` e
`execute`.

**Versão do protocolo.** O gateway aceita somente a versão `2026-07-28` do protocolo MCP e responde
qualquer outra versão com HTTP 400 informando a versão suportada. Um cliente que não fale essa versão
falha já na primeira requisição. Esses comandos de cliente vêm da CLI de cada cliente e do portal, e
não foram testados contra versões específicas dos clientes.

### Notas de produção

- O gateway serve HTTP puro. Coloque na frente um reverse proxy que termine o TLS e defina
  `MCPGW_PUBLIC_URL` com a origem `https` externa. Ela é o issuer do OAuth, e o https ativa cookies
  `Secure` e HSTS.
- Encaminhe ao gateway todos os caminhos da tabela abaixo, não só o `/mcp`.
- O `X-Forwarded-For` não é considerado confiável, então os rate limits por IP enxergam o endereço do
  proxy.
- Rode o gateway com um usuário sem privilégios e mantenha o `.env` com modo `0600`.

## Configuração

Todas as configurações são variáveis de ambiente. O `.env.example` lista as quatro primeiras.

| Variável | Obrigatória | Padrão | Descrição |
| --- | --- | --- | --- |
| `MCPGW_DATABASE_URL` | sim | nenhum | URL do PostgreSQL. O pool tem 16 conexões por padrão, e `pool_max_conns` na URL substitui esse valor. |
| `MCPGW_MASTER_KEY` | para o `serve` | nenhum | Base64 de 32 bytes aleatórios. Cifra todos os segredos armazenados. Os comandos de admin que cifram ou abrem segredos também precisam dela. |
| `MCPGW_LISTEN_ADDR` | não | `127.0.0.1:8787` | `host:port` em que o gateway escuta. |
| `MCPGW_PUBLIC_URL` | não | `http://localhost:8787` | Origem externa, sem path. É o issuer do OAuth e a base de todos os links que o gateway monta. |
| `MCPGW_PPROF_ADDR` | não | não definida | `host:port` de loopback que serve o `net/http/pprof`. Valores fora do loopback são rejeitados. |
| `MCPGW_ADMIN_PASSWORD` | não | nenhum | Lida apenas por `mcpgw admin user create`. Se ausente, usa a primeira linha do stdin. |
| `NO_COLOR` | não | não definida | Qualquer valor desliga as cores do banner de inicialização. |

O que o gateway serve na sua única porta:

| Caminho | Finalidade |
| --- | --- |
| `/mcp` | O endpoint MCP para os agentes. |
| `/authorize`, `/token`, `/register`, `/jwks.json` | Authorization server OAuth para clientes MCP. |
| `/.well-known/oauth-authorization-server`, `/.well-known/oauth-protected-resource` e `/.well-known/oauth-protected-resource/mcp` | Documentos de discovery do OAuth. |
| `/connect/{slug}`, `/connect/callback`, `/oauth/client-metadata.json` | O gateway conectando a conta de um usuário a um servidor upstream. |
| `/admin/`, `/admin/api/` | Interface web de admin e a API dela. |
| `/_next/` e alguns arquivos de marca na raiz, como `/favicon.ico` | Assets estáticos da interface web. |
| `/portal/`, `/portal/api/` | Portal do usuário e a API dele. |
| `/healthz` | Health check. |

## Engines de sandbox

O code mode executa scripts em um sandbox. Existem dois engines:

- `MCPGW_SANDBOX_ENGINE`: `qjs` (padrão, QuickJS em WebAssembly) ou `monty` (Python em um worker Rust nativo).
- `MCPGW_MONTYRS_WORKER`: caminho do worker usado pelo `monty`. Padrão: `montyrs-worker` ao lado do executável `mcpgw`, resolvido para caminho absoluto.
- `make worker` compila o worker em `bin/montyrs-worker` (`.exe` no Windows). O `make build` também o executa.
- No Windows o worker roda dentro de um AppContainer, que precisa de leitura e execução no exe: o `make worker` concede; para um worker copiado para outro lugar rode `icacls montyrs-worker.exe /grant "*S-1-15-2-1:(RX)"`.

Segurança por plataforma: o worker se confina no nível do sistema operacional.

- Linux (mais forte): allowlist de syscalls (seccomp), `no_new_privs` e rlimits.
- Windows: AppContainer, Job Object e mitigation policies do processo.
- macOS: o Seatbelt nega rede e acesso a arquivos, mas não filtra syscalls nem impede memória executável (W^X é apenas o padrão do SO). Um escape completo exige quebrar o interpretador Monty e depois o Seatbelt.

Prefira Linux em produção multi-tenant.

## Web admin e portal

A interface é um static export do Next.js embutido no binário (`web/admin`), servido na mesma origem
da API.

- `/admin/` é para usuários admin. Gerencia servidores (adicionar, testar, sincronizar, tokens de
  header, clientes OAuth), revisão de tools, ceilings por usuário, usuários, clientes OAuth e
  consents, conexões compartilhadas, logs de execução e o log de ações de admin. Escritas que
  armazenam segredos pedem a sua senha novamente.
- `/portal/` é para todos os usuários. Conecta as suas próprias contas aos servidores por OAuth ou
  colando um token, suporta profiles nomeados e mostra como instalar o gateway em cada agente.

**Idioma.** A interface web, as páginas de login e de consent e as mensagens de erro da API estão
disponíveis em inglês e em português do Brasil. O idioma do navegador define o padrão
(`Accept-Language`), e um seletor na barra lateral e nas telas de login permite trocar. A CLI segue
`LANG`/`LC_ALL`. Mensagens lidas por agentes (erros do code mode, descrições de tools) ficam só em inglês.

## Visão geral da CLI

```text
mcpgw serve      roda o gateway
mcpgw migrate    aplica as migrations pendentes do banco
mcpgw admin      comandos de administração
```

Rode `bin/mcpgw admin` para imprimir o uso completo.

| Grupo | Comandos |
| --- | --- |
| `user` | `create`, `list`, `set-admin` |
| `server` | `add`, `list`, `enable`, `disable`, `remove`, `set-header`, `set-header-format`, `set-oauth-client`, `set-trust`, `set-env`, `set-credential-env`, `set-connection-env`, `set-connection-url` |
| `ceiling` | `set`, `list` |
| `tool` | `list`, `show`, `level`, `approve` |
| `catalog` | `sync` |

Segredos nunca passam pelo argv. Flags como `--value-env`, `--secret-env` e `--env NAME=VAR` indicam
o nome de uma variável de ambiente que guarda o valor.

## Desenvolvimento

```bash
make build            # interface web e depois o bin/mcpgw
make admin            # recompila apenas a interface web embutida
make test             # testes em Go
make test-admin-web   # testes unitários da interface web
make lint             # checagem do gofmt e go vet
make fmt              # gofmt
```

Os testes que precisam de Postgres são pulados, a menos que `MCPGW_DATABASE_URL` esteja definida.
Eles criam e removem bancos descartáveis, então use um role que possa executar `CREATE DATABASE`.

Para entender como as peças se encaixam, leia [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) (em
inglês). As design specs ficam em [docs/specs/](docs/specs/).

## Comunidade

- [CONTRIBUTING.md](CONTRIBUTING.md)
- [SECURITY.md](SECURITY.md)
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- [README.md](README.md), a versão em inglês deste arquivo

## Licença

Licenciado sob a [Apache License 2.0](LICENSE). O [NOTICE](NOTICE) cobre o `third_party/qjs`, uma
cópia com patch do [fastschema/qjs](https://github.com/fastschema/qjs) e o build WebAssembly do
QuickJS-ng que ele inclui, ambos sob a licença MIT.
