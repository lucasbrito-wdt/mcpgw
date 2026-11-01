import { test as base, type Browser } from '@playwright/test';

export { expect } from '@playwright/test';
export type { Page } from '@playwright/test';

/**
 * With E2E_CDP_URL set (e.g. `obscura --allow-private-network serve` on http://127.0.0.1:9222),
 * the specs drive that browser over CDP instead of launching Chromium.
 */
export const test = base.extend<object, { browser: Browser }>({
  browser: [
    async ({ playwright, launchOptions }, use) => {
      const cdp = process.env.E2E_CDP_URL;
      const browser = cdp ? await playwright.chromium.connectOverCDP(cdp) : await playwright.chromium.launch(launchOptions);
      await use(browser);
      await browser.close();
    },
    { scope: 'worker' },
  ],
});
