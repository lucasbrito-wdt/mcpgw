import { expect, test, type Page } from './fixtures';

/** Collects CSP violations reported to the console; the test asserts the list is empty at the end. */
function trackCsp(page: Page): string[] {
  const violations: string[] = [];
  page.on('console', (msg) => {
    const text = msg.text();
    if (text.includes('Content Security Policy') || text.includes('Refused to')) violations.push(text);
  });
  return violations;
}

test('admin logs in, approves a pending tool and revokes a consent', async ({ page }) => {
  const csp = trackCsp(page);

  await test.step('anonymous visit redirects to login, then signs in', async () => {
    await page.goto('/admin/');
    await expect(page).toHaveURL(/\/admin\/login\//);
    await page.getByLabel('E-mail').fill('admin@example.com');
    await page.getByLabel('Senha').fill(process.env.E2E_ADMIN_PASSWORD!);
    await page.getByRole('button', { name: 'Entrar' }).click();
    await expect(page.getByRole('heading', { level: 1, name: 'Visão geral' })).toBeVisible();
  });

  await test.step('approves the pending tool', async () => {
    await page.getByRole('link', { name: /^Revisão/ }).click();
    await expect(page.getByRole('heading', { level: 1, name: 'Revisão' })).toBeVisible();
    await page.getByRole('button', { name: 'Aprovar', exact: true }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Aprovar e liberar' }).click();
    await expect(page.getByText(/Hash aprovado\. fake\.get_item liberada/)).toBeVisible();
    await expect(page.getByText('Nada esperando revisão')).toBeVisible();
  });

  await test.step('revokes the seeded consent', async () => {
    await page.getByRole('link', { name: 'Clientes', exact: true }).click();
    await page.getByRole('tab', { name: 'Consentimentos' }).click();
    await page.getByRole('button', { name: /^Revogar consentimento de ana@example\.com/ }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Revogar', exact: true }).click();
    await expect(page.getByText('Acesso de E2E Agent revogado.')).toBeVisible();
    await expect(page.getByText('Revogado', { exact: true })).toBeVisible();
  });

  expect.soft(csp, 'CSP violations in the console').toEqual([]);
});
