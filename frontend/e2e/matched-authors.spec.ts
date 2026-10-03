import { test, expect } from './_fixtures';

/**
 * Запрос по имени автора (#290): над выдачей /books — плашка автора со ссылкой
 * на его карточку. Длинное имя не должно распирать страницу на телефоне —
 * проверка вёрстки, поэтому e2e.
 */
test('плашка автора над выдачей ведёт на карточку и не ломает вёрстку', async ({ mockedPage: page }) => {
  await page.route(/\/api\/books(\?|$)/, (route) => {
    const url = new URL(route.request().url());
    const q = url.searchParams.get('q') ?? '';
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [{ id: 101, title: 'Война и мир', authors: ['Толстой Лев Николаевич'], genres: [], lib_id: '101' }],
        total: 1,
        limit: 20,
        offset: 0,
        processing_ms: 1,
        facets: {},
        matched_authors: q
          ? [{ id: 17, full_name: 'Толстой Лев Николаевич', book_count: 595 }]
          : undefined,
      }),
    });
  });
  await page.setViewportSize({ width: 375, height: 812 });
  await page.goto('/books?q=толстой');

  const link = page.getByRole('link', { name: /Толстой Лев Николаевич.*595 книг/ });
  await expect(link).toBeVisible({ timeout: 10_000 });
  await expect(link).toHaveAttribute('href', '/authors/17');
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(overflow).toBeLessThanOrEqual(0);

  // Без запроса плашки нет.
  await page.goto('/books');
  await expect(page.getByRole('link', { name: 'Война и мир', exact: true })).toBeVisible({ timeout: 10_000 });
  await expect(page.getByRole('navigation', { name: 'Авторы по запросу' })).toHaveCount(0);
});
