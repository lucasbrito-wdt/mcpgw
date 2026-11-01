import { expect, test, type Page } from './fixtures';

const GOOD_TOKEN = 'good-token-1234';

/** Collects CSP violations reported to the console; the test asserts the list is empty at the end. */
function trackCsp(page: Page): string[] {
  const violations: string[] = [];
  page.on('console', (msg) => {
    const text = msg.text();
    if (text.includes('Content Security Policy') || text.includes('Refused to')) violations.push(text);
  });
  return violations;
}

test('user connects a token server, sees it connected, disconnects', async ({ page }) => {
  const csp = trackCsp(page);

  await test.step('signs in', async () => {
    await page.goto('/portal/');
    await expect(page).toHaveURL(/\/portal\/login\//);
    await page.getByLabel('E-mail').fill('ana@example.com');
    await page.getByLabel('Senha').fill(process.env.E2E_USER_PASSWORD!);
    await page.getByRole('button', { name: 'Entrar' }).click();
    await expect(page).toHaveURL(/\/portal\/(\?.*)?$/);
  });

  await test.step('opens the token server, not connected yet', async () => {
    await page.getByRole('link', { name: /fakeauth/i }).first().click();
    await expect(page).toHaveURL(/\/portal\/connect\/\?slug=fakeauth/);
    await expect(page.getByText('Usando a compartilhada do admin')).toHaveCount(0);
    await expect(page.getByText('Não conectada').first()).toBeVisible();
  });

  const dialog = page.getByRole('dialog');
  await test.step('a non-admin has no shared option', async () => {
    await expect(page.getByRole('button', { name: 'Escolher para quem conectar' })).toHaveCount(0);
    // Hovering the button swaps the route hero (preview) and can swallow a click made in the same frame:
    // retry until the dialog is open (reported as a product issue, see route-hero.tsx).
    await expect(async () => {
      await page.getByRole('button', { name: /^Conectar conta/ }).click();
      await expect(dialog).toBeVisible({ timeout: 2_000 });
    }).toPass();
    await expect(dialog.getByLabel('Confirme a sua senha do mcpgw')).toHaveCount(0);
  });

  await test.step('a refused token is reported, a good one is accepted', async () => {
    await dialog.getByLabel('Token', { exact: true }).fill('good-wrong');
    await dialog.getByRole('button', { name: 'Testar' }).click();
    await expect(dialog.getByText('O servidor recusou o token (HTTP 401).')).toBeVisible();
    await dialog.getByLabel('Token', { exact: true }).fill(GOOD_TOKEN);
    await dialog.getByRole('button', { name: 'Testar' }).click();
    await expect(dialog.getByText(/Token aceito/)).toBeVisible();
  });

  await test.step('saves and shows only the last 4 characters', async () => {
    await dialog.getByRole('button', { name: 'Salvar e usar' }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page.getByText('Usando a sua chave')).toBeVisible();
    await expect(page.getByText('••••1234').first()).toBeVisible();
    await expect(page.locator('body')).not.toContainText(GOOD_TOKEN);
  });

  await test.step('disconnects', async () => {
    await page.getByRole('button', { name: 'Desconectar', exact: true }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Desconectar', exact: true }).click();
    await expect(page.getByText('Não conectada').first()).toBeVisible();
    await expect(page.getByText('Usando a sua chave')).toHaveCount(0);
  });

  expect.soft(csp, 'CSP violations in the console').toEqual([]);
});
