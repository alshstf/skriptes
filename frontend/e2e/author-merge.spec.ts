import { test, expect } from './_fixtures';

/**
 * Дубли авторов (#308): подсказка админу на карточке автора, диалог слияния с
 * выбором оставляемой записи, страница «Дубли авторов» в админке.
 */
const dup = {
  id: 42,
  full_name: 'Алексеев Евгений',
  book_count: 2,
  renown: 0,
  years_active: { from: 2001, to: 2003 },
  reason: 'middle_name',
};

test('подсказка на карточке автора и слияние в запись с бо́льшим числом книг', async ({ mockedPage: page }) => {
  await page.route(/\/api\/admin\/authors\/17\/duplicates$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [dup] }) }),
  );
  let mergeBody: unknown = null;
  let mergeUrl = '';
  await page.route(/\/api\/admin\/authors\/\d+\/merge$/, (route) => {
    mergeUrl = route.request().url();
    mergeBody = route.request().postDataJSON();
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ target_id: 17, works: 2 }) });
  });

  await page.goto('/authors/17');
  await expect(page.getByText('Возможно, это тот же автор')).toBeVisible({ timeout: 10_000 });
  await expect(page.getByRole('link', { name: 'Алексеев Евгений', exact: true })).toHaveAttribute('href', '/authors/42');
  await expect(page.getByText(/2 книги · 2001–2003 · та же фамилия и имя/)).toBeVisible();

  await page.getByRole('button', { name: 'Объединить…' }).click();
  // По умолчанию остаётся запись с бо́льшим числом книг — текущая (5 книг).
  await expect(page.getByRole('button', { name: /^Алексеев Евгений Артёмович/ })).toHaveAttribute('aria-pressed', 'true');
  await page.getByRole('button', { name: 'Оставить «Алексеев Евгений Артёмович»' }).click();
  await expect.poll(() => mergeUrl).toContain('/api/admin/authors/42/merge');
  expect(mergeBody).toEqual({ target_id: 17 });
});

test('страница «Дубли авторов» в админке', async ({ mockedPage: page }) => {
  await page.route(/\/api\/admin\/authors\/duplicates(\?|$)/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [{ a: { ...dup, id: 1, full_name: 'Лукьяненко Сергей', book_count: 8 }, b: { ...dup, id: 2, full_name: 'Лукьяненко Сергей Васильевич', book_count: 314, renown: 1989 } }],
        total: 1,
      }),
    }),
  );
  await page.setViewportSize({ width: 375, height: 812 });
  await page.goto('/admin/authors');
  await expect(page.getByRole('heading', { name: 'Дубли авторов' })).toBeVisible({ timeout: 10_000 });
  await expect(page.getByRole('link', { name: 'Лукьяненко Сергей Васильевич' })).toBeVisible();
  await expect(page.getByText(/^1 пара$/)).toBeVisible();
  await expect(page.getByText('та же фамилия и имя', { exact: true })).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(overflow).toBeLessThanOrEqual(0);
});

// «Не дубликат» (#527): разные люди с теми же ФИ — пара запоминается на сервере и
// сразу уходит и с карточки автора, и со страницы «Дубли авторов».
test('«Не дубликат» на карточке автора и на странице дублей', async ({ mockedPage: page }) => {
  await page.route(/\/api\/admin\/authors\/17\/duplicates$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [dup] }) }),
  );
  const calls: Array<{ url: string; body: unknown }> = [];
  await page.route(/\/api\/admin\/authors\/\d+\/not-duplicate$/, (route) => {
    calls.push({ url: route.request().url(), body: route.request().postDataJSON() });
    void route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ rejected: true }) });
  });

  await page.goto('/authors/17');
  await expect(page.getByText('Возможно, это тот же автор')).toBeVisible({ timeout: 10_000 });
  await page.getByRole('button', { name: 'Не дубликат' }).click();
  await expect.poll(() => calls.length).toBe(1);
  expect(calls[0].url).toContain('/api/admin/authors/17/not-duplicate');
  expect(calls[0].body).toEqual({ other_id: 42 });
  await expect(page.getByText('Возможно, это тот же автор')).toHaveCount(0);

  await page.route(/\/api\/admin\/authors\/duplicates(\?|$)/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [
          { a: { ...dup, id: 1, full_name: 'Матесон Ричард', book_count: 40 }, b: { ...dup, id: 2, full_name: 'Матесон Ричард Кристиан', book_count: 3 } },
          { a: { ...dup, id: 3, full_name: 'Лукьяненко Сергей', book_count: 8, evidence: 'series' }, b: { ...dup, id: 4, full_name: 'Лукьяненко Сергей Васильевич', book_count: 314, evidence: 'series' } },
        ],
        total: 2,
      }),
    }),
  );
  await page.setViewportSize({ width: 375, height: 812 });
  await page.goto('/admin/authors');
  await expect(page.getByText(/^2 пары$/)).toBeVisible({ timeout: 10_000 });
  await page.locator('li', { hasText: 'Матесон Ричард Кристиан' }).getByRole('button', { name: 'Не дубликат' }).click();
  await expect.poll(() => calls.length).toBe(2);
  expect(calls[1].url).toContain('/api/admin/authors/1/not-duplicate');
  expect(calls[1].body).toEqual({ other_id: 2 });
  await expect(page.getByRole('link', { name: 'Матесон Ричард Кристиан' })).toHaveCount(0);
  await expect(page.getByText(/^1 пара$/)).toBeVisible();
  await expect(page.getByRole('link', { name: 'Лукьяненко Сергей Васильевич' })).toBeVisible();
  // Причина и доказательство (#527) — в строке пары.
  await expect(page.getByText('та же фамилия и имя, книги одного цикла')).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(overflow).toBeLessThanOrEqual(0);
});
