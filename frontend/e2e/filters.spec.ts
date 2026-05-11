import { test, expect } from './_fixtures';

test('filters: genre checkbox writes to URL and re-issues /api/books with filter', async ({
  mockedPage: page,
}) => {
  // Перехватываем все запросы к /api/books (без id) — для каждого
  // запоминаем URL, чтобы потом проверить, что фильтр действительно ушёл.
  const calls: string[] = [];
  await page.route(/\/api\/books\?/, (route) => {
    calls.push(route.request().url());
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [],
        total: 0,
        limit: 20,
        offset: 0,
        processing_ms: 1,
        facets: { genres: { sf_action: 4, fantasy: 2 }, lang: { ru: 6 } },
      }),
    });
  });

  await page.goto('/books');
  // Дожидаемся, что сайдбар фильтров отрисовался.
  await expect(page.getByRole('heading', { name: 'Фильтры' })).toBeVisible({ timeout: 10_000 });

  // Жанр "sf_action" из facets (с counter=4) должен быть в чек-боксах.
  const genreCheckbox = page.getByRole('checkbox', { name: /sf_action/ });
  await expect(genreCheckbox).toBeVisible();
  await genreCheckbox.check();

  // URL должен содержать sf_action (TanStack Router сериализует массивы
  // как JSON, поэтому ожидаем %22sf_action%22 — "sf_action" url-encoded).
  await expect(page).toHaveURL(/sf_action/);

  // Должен прилететь новый запрос с genres=sf_action.
  await expect.poll(() => calls.some((c) => c.includes('genres=sf_action'))).toBe(true);

  // Чип активного фильтра появился над списком.
  await expect(page.getByText(/Жанр: sf_action/)).toBeVisible();
});

test('filters: sort dropdown changes URL', async ({ mockedPage: page }) => {
  await page.goto('/books');
  await expect(page.getByLabel('Сортировка')).toBeVisible({ timeout: 10_000 });
  await page.getByLabel('Сортировка').selectOption('year_desc');
  await expect(page).toHaveURL(/sort=year_desc/);
});
