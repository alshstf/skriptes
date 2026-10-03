import { test, expect } from './_fixtures';

/**
 * Большая издательская серия (#311): книги рисуются порциями по 100 с кнопкой
 * «Показать ещё»; у межавторской серии — выбор сортировки.
 */
const N = 250;
const big = {
  id: 77,
  title: 'Панорама романов о любви',
  kind: 'multi',
  book_count: N,
  authors: [{ id: 1, name: 'Автор Один' }],
  books: Array.from({ length: N }, (_, i) => ({
    id: 10_000 + i,
    work_id: 20_000 + i,
    title: `Роман ${String(N - i).padStart(3, '0')}`,
    authors: ['Автор Один'],
    genres: [],
    lang: 'ru',
    series: 'Панорама романов о любви',
    series_id: 77,
    ser_no: i + 1,
    series_order: i + 1,
    year: 1990 + (i % 30),
  })),
};

test('series: большая межавторская серия — порции по 100 и сортировка', async ({ mockedPage: page }) => {
  await page.route(/\/api\/series\/77$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(big) }),
  );
  await page.goto('/series/77');

  const items = page.locator('main ul.space-y-1 > li');
  await expect(items).toHaveCount(100);
  await expect(page.getByText('Показано 100 из 250')).toBeVisible();
  await page.getByRole('button', { name: 'Показать ещё 100' }).click();
  await expect(items).toHaveCount(200);
  await page.getByRole('button', { name: 'Показать ещё 50' }).click();
  await expect(items).toHaveCount(250);
  await expect(page.getByRole('button', { name: /Показать ещё/ })).toHaveCount(0);

  // По номеру первой идёт №1 («Роман 250»); по названию — «Роман 001».
  await expect(items.first()).toContainText('Роман 250');
  await page.getByLabel('Сортировка').selectOption('title');
  await expect(items.first()).toContainText('Роман 001');
  await expect(items).toHaveCount(100);
});
