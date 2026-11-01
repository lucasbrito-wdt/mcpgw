import { existsSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

import { defineConfig, devices } from '@playwright/test';

// The pinned headless shell (1187) is incomplete in the local cache and browsers are never reinstalled:
// default to the Chromium 1248 already there. E2E_CHROMIUM_PATH overrides; neither set = Playwright's own.
const CACHED_CHROMIUM = join(
  homedir(),
  'Library/Caches/ms-playwright/chromium-1248/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing',
);
const chromiumPath = process.env.E2E_CHROMIUM_PATH || (existsSync(CACHED_CHROMIUM) ? CACHED_CHROMIUM : undefined);

export default defineConfig({
  testDir: './e2e',
  globalSetup: './e2e/global-setup.ts',
  workers: 1,
  fullyParallel: false,
  retries: 0,
  use: { baseURL: 'http://127.0.0.1:18787', trace: 'retain-on-failure', locale: 'pt-BR' },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'], launchOptions: { executablePath: chromiumPath } },
    },
  ],
});
