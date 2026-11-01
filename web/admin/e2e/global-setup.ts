import { ChildProcess, execFileSync, spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { fileURLToPath } from 'node:url';

import { request, type FullConfig } from '@playwright/test';

const here = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(here, '../../..');
const DB_NAME = 'mcpgw_e2e';
const GATEWAY_ADDR = '127.0.0.1:18787';
const PUBLIC_URL = `http://${GATEWAY_ADDR}`;
const FAKE_ADDR = '127.0.0.1:18795';
export const FAKE_TOKEN = 'good-token-1234';

/** Reads MCPGW_DATABASE_URL from the environment or the project .env (never printed or logged). */
function sourceDatabaseUrl(): URL {
  let raw = process.env.MCPGW_DATABASE_URL ?? '';
  const envFile = path.join(repoRoot, '.env');
  if (!raw && fs.existsSync(envFile)) {
    const line = fs.readFileSync(envFile, 'utf8').split('\n').find((l) => l.startsWith('MCPGW_DATABASE_URL='));
    raw = (line ?? '').slice('MCPGW_DATABASE_URL='.length).trim().replace(/^["']|["']$/g, '');
  }
  if (!raw) throw new Error('MCPGW_DATABASE_URL is not set (environment or .env)');
  return new URL(raw);
}

function psqlBinary(): string {
  if (process.env.PSQL) return process.env.PSQL;
  for (const dir of (process.env.PATH ?? '').split(path.delimiter)) {
    if (dir && fs.existsSync(path.join(dir, 'psql'))) return path.join(dir, 'psql');
  }
  return '/opt/homebrew/opt/libpq/bin/psql';
}

/** Runs psql with libpq env vars so the password never lands in argv. */
function psql(base: URL, db: string, args: string[]): void {
  execFileSync(psqlBinary(), ['-v', 'ON_ERROR_STOP=1', '-q', '-d', db, ...args], {
    env: {
      ...process.env,
      PGHOST: base.hostname,
      PGPORT: base.port || '5432',
      PGUSER: decodeURIComponent(base.username),
      PGPASSWORD: decodeURIComponent(base.password),
      PGSSLMODE: base.searchParams.get('sslmode') ?? 'prefer',
    },
    stdio: ['ignore', 'ignore', 'inherit'],
  });
}

function run(cmd: string, args: string[], opts: { cwd?: string; env?: NodeJS.ProcessEnv } = {}): void {
  execFileSync(cmd, args, { cwd: opts.cwd ?? repoRoot, env: opts.env ?? process.env, stdio: ['ignore', 'inherit', 'inherit'] });
}

async function waitFor(url: string, proc: ChildProcess, label: string): Promise<void> {
  const deadline = Date.now() + 60_000;
  while (Date.now() < deadline) {
    if (proc.exitCode !== null) throw new Error(`${label} exited early with code ${proc.exitCode}`);
    try {
      const res = await fetch(url);
      if (res.status < 500) return;
    } catch {
      // not listening yet
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`${label} not ready at ${url}`);
}

function stop(proc: ChildProcess | undefined): Promise<void> {
  if (!proc || proc.exitCode !== null) return Promise.resolve();
  return new Promise((resolve) => {
    proc.once('exit', () => resolve());
    proc.kill('SIGTERM');
    setTimeout(() => proc.kill('SIGKILL'), 5_000).unref();
  });
}

export default async function globalSetup(_config: FullConfig): Promise<() => Promise<void>> {
  const base = sourceDatabaseUrl();
  const e2eUrl = new URL(base.toString());
  e2eUrl.pathname = `/${DB_NAME}`;

  const adminPassword = randomBytes(12).toString('hex') + 'Xy1';
  const userPassword = randomBytes(12).toString('hex') + 'Xy1';
  process.env.E2E_ADMIN_PASSWORD = adminPassword;
  process.env.E2E_USER_PASSWORD = userPassword;

  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'mcpgw-e2e-'));
  const gatewayBin = path.join(tmp, 'mcpgw');
  const fakeBin = path.join(tmp, 'fakeup');
  const procs: ChildProcess[] = [];

  const teardown = async () => {
    await Promise.all(procs.map(stop));
    try {
      psql(base, 'postgres', ['-c', `DROP DATABASE IF EXISTS ${DB_NAME} WITH (FORCE)`]);
    } catch (e) {
      console.error('e2e teardown: could not drop test database', e);
    }
    if (!process.env.E2E_KEEP_LOGS) {
      for (const f of fs.readdirSync(tmp)) fs.unlinkSync(path.join(tmp, f));
      fs.rmdirSync(tmp);
    }
  };

  try {
    psql(base, 'postgres', ['-c', `DROP DATABASE IF EXISTS ${DB_NAME} WITH (FORCE)`, '-c', `CREATE DATABASE ${DB_NAME}`]);

    if (!process.env.E2E_SKIP_ADMIN_BUILD) run('make', ['admin']);
    run('go', ['build', '-o', gatewayBin, './cmd/mcpgw']);
    run('go', ['build', '-o', fakeBin, './web/admin/e2e/fakeupstream']);

    const env: NodeJS.ProcessEnv = {
      ...process.env,
      MCPGW_DATABASE_URL: e2eUrl.toString(),
      MCPGW_LISTEN_ADDR: GATEWAY_ADDR,
      MCPGW_PUBLIC_URL: PUBLIC_URL,
      MCPGW_MASTER_KEY: randomBytes(32).toString('base64'),
    };
    const spawnLogged = (bin: string, args: string[], name: string, e: NodeJS.ProcessEnv) => {
      const log = fs.openSync(path.join(tmp, `${name}.log`), 'w');
      const p = spawn(bin, args, { cwd: tmp, env: e, stdio: ['ignore', log, log] });
      procs.push(p);
      return p;
    };

    const fake = spawnLogged(fakeBin, ['-addr', FAKE_ADDR, '-token', FAKE_TOKEN], 'fakeup', process.env);
    const gateway = spawnLogged(gatewayBin, ['serve'], 'mcpgw', env);
    await waitFor(`http://${FAKE_ADDR}/mcp`, fake, 'fakeup');
    await waitFor(`${PUBLIC_URL}/admin/`, gateway, 'mcpgw serve');

    const cli = (args: string[], password?: string) =>
      run(gatewayBin, ['admin', ...args], { cwd: tmp, env: password ? { ...env, MCPGW_ADMIN_PASSWORD: password } : env });
    cli(['user', 'create', '--admin', '--email', 'admin@example.com'], adminPassword);
    cli(['user', 'create', '--email', 'ana@example.com'], userPassword);
    cli(['server', 'add', '--slug', 'fake', '--url', `http://${FAKE_ADDR}/mcp`, '--auth', 'none']);
    cli(['catalog', 'sync', '--slug', 'fake']);
    cli(['ceiling', 'set', '--email', 'ana@example.com', '--slug', 'fake', '--level', 'read']);

    // The header config (prefix, format hint) has no CLI flag, so fakeauth goes through the admin API.
    const api = await request.newContext({ baseURL: PUBLIC_URL });
    const login = await api.post('/admin/api/session', { headers: { Origin: PUBLIC_URL }, data: { email: 'admin@example.com', password: adminPassword } });
    if (!login.ok()) throw new Error(`admin login failed: HTTP ${login.status()}`);
    const csrf = (await api.storageState()).cookies.find((c) => c.name.endsWith('mcpgw_admin_csrf'))?.value;
    if (!csrf) throw new Error('csrf cookie missing after admin login');
    const headers = { Origin: PUBLIC_URL, 'X-CSRF-Token': csrf };

    const created = await api.post('/admin/api/servers', {
      headers,
      data: {
        slug: 'fakeauth',
        url: `http://${FAKE_ADDR}/auth/mcp`,
        auth_kind: 'header',
        transport: 'auto',
        category: 'dev',
        header: { name: 'Authorization', prefix: 'Bearer ', format_hint: 'good-' },
      },
    });
    if (!created.ok()) throw new Error(`create fakeauth failed: HTTP ${created.status()} ${await created.text()}`);

    const users = (await (await api.get('/admin/api/users?limit=200', { headers })).json()) as { items: { id: string; email: string }[] };
    const ana = users.items.find((u) => u.email === 'ana@example.com');
    if (!ana) throw new Error('ana not found in /admin/api/users');
    const ceiling = await api.put(`/admin/api/ceilings/${ana.id}/fakeauth`, { headers, data: { level: 'read' } });
    if (!ceiling.ok()) throw new Error(`ceiling fakeauth failed: HTTP ${ceiling.status()} ${await ceiling.text()}`);
    await api.dispose();

    psql(e2eUrl, DB_NAME, ['-f', path.join(here, 'seed.sql')]);
  } catch (e) {
    await teardown();
    throw e;
  }
  return teardown;
}
