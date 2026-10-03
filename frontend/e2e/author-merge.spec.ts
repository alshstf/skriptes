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
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(overflow).toBeLessThanOrEqual(0);
});
