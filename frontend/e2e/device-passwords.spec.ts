import { test, expect } from './_fixtures';

// Пароли устройств (#389): создание показывает пароль один раз (группами),
// список — последнее использование, отзыв — DELETE.

test('профиль: пароли устройств', async ({ mockedPage: page }) => {
  await page.route(/\/api\/me\/kindle-targets$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '{"items":[]}' }),
  );
  let devices = [
    { id: 1, name: 'Kobo', created_at: '2026-10-01T10:00:00Z', last_used_at: '2026-10-06T10:00:00Z' },
  ];
  let posted: unknown = null;
  let deleted = '';
  await page.route(/\/api\/me\/devices(\/\d+)?$/, async (route) => {
    const req = route.request();
    if (req.method() === 'POST') {
      posted = req.postDataJSON();
      const dev = { id: 2, name: 'Readest', created_at: '2026-10-07T01:00:00Z' };
      devices = [dev, ...devices];
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ device: dev, password: 'abcdefghjkmnpqrs', login: 'tester@example.com' }),
      });
      return;
    }
    if (req.method() === 'DELETE') {
      deleted = req.url();
      devices = devices.filter((d) => !req.url().endsWith(`/${d.id}`));
      await route.fulfill({ status: 204 });
      return;
    }
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: devices }) });
  });

  await page.goto('/me');
  const card = page.locator('div').filter({ has: page.getByText('Пароли устройств', { exact: true }) }).last();
  await expect(page.getByText('Пароли устройств', { exact: true })).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText(/последний вход 6 октября 2026/)).toBeVisible();

  await page.getByLabel('Название устройства').fill('Readest');
  await page.getByRole('button', { name: 'Создать пароль' }).click();
  await expect(page.getByLabel('Пароль устройства')).toHaveText('abcd efgh jkmn pqrs');
  expect(posted).toEqual({ name: 'Readest' });
  await expect(page.getByText(/ещё не входил/)).toBeVisible();

  page.once('dialog', (d) => void d.accept());
  await page.getByRole('button', { name: 'Отозвать пароль «Kobo»' }).click();
  await expect.poll(() => deleted).toMatch(/\/api\/me\/devices\/1$/);
  await expect(card.getByText('Kobo')).toHaveCount(0);
});
