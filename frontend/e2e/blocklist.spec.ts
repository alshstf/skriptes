import { test, expect, authorDetailFixture } from './_fixtures';

// «Не предлагать» (план recsys/03, 3.2): кнопка у колокольчика на карточке
// автора → POST /api/authors/17/block (и обратно DELETE); в профиле — список с
// «Вернуть».

test('blocklist: кнопка на карточке автора', async ({ mockedPage: page }) => {
  let blocked = false;
  await page.route(/\/api\/authors\/17$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ...authorDetailFixture, is_blocked: blocked }),
    }),
  );
  const calls: string[] = [];
  await page.route(/\/api\/authors\/17\/block$/, (route) => {
    calls.push(route.request().method());
    blocked = route.request().method() === 'POST';
    void route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ is_blocked: blocked }) });
  });
  await page.goto('/authors/17');
  const btn = page.getByRole('button', { name: 'Не предлагать автора' });
  await expect(btn).toBeVisible({ timeout: 10_000 });
  await btn.click();
  await expect.poll(() => calls).toEqual(['POST']);
  const undo = page.getByRole('button', { name: 'Снова предлагать автора' });
  await expect(undo).toBeVisible();
  await undo.click();
  await expect.poll(() => calls).toEqual(['POST', 'DELETE']);
  await expect(page.getByRole('button', { name: 'Не предлагать автора' })).toBeVisible();
});

test('blocklist: список в профиле и «Вернуть»', async ({ mockedPage: page }) => {
  let list = {
    authors: [{ id: 17, full_name: 'Алексеев Евгений Артёмович', added_at: '2026-10-01T00:00:00Z' }],
    series: [{ id: 7, title: 'Петля', author_name: 'Алексеев Евгений', added_at: '2026-10-02T00:00:00Z' }],
  };
  await page.route(/\/api\/me\/blocklist$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(list) }),
  );
  await page.route(/\/api\/me\/kindle-targets$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '{"items":[]}' }),
  );
  const calls: string[] = [];
  await page.route(/\/api\/series\/7\/block$/, (route) => {
    calls.push(route.request().method());
    list = { ...list, series: [] };
    void route.fulfill({ status: 200, contentType: 'application/json', body: '{"is_blocked":false}' });
  });
  await page.goto('/me');
  const card = page.getByTestId('blocklist');
  await expect(card).toBeVisible({ timeout: 10_000 });
  await expect(card.getByRole('link', { name: 'Алексеев Евгений Артёмович' })).toHaveAttribute('href', '/authors/17');
  await expect(card.getByRole('link', { name: /Петля/ })).toHaveAttribute('href', '/series/7');
  await card.getByRole('button', { name: 'Снова предлагать цикл Петля' }).click();
  await expect.poll(() => calls).toEqual(['DELETE']);
  await expect(card.getByRole('link', { name: /Петля/ })).toHaveCount(0);
});
