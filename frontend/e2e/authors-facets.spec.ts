import { test, expect } from './_fixtures';

// Счётчики фильтров /authors (#389): число авторов у жанров, категории (не сумма
// жанров — автор в двух жанрах категории считается один раз), языков и
// экранизаций; параметры фильтров уходят и в запрос счётчиков.

test('авторы: числа авторов в фильтрах', async ({ mockedPage: page }) => {
  await page.route(/\/api\/authors(\?|$)/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ items: [{ id: 17, full_name: 'Кинг Стивен', book_count: 3, is_favorite: false, favorited_books_count: 0, has_adaptations: true, reader_rating_count: 0 }], total: 1 }),
    }),
  );
  const facetRequests: string[] = [];
  await page.route(/\/api\/authors\/facets(\?|$)/, (route) => {
    facetRequests.push(route.request().url());
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        genres: { sf_action: 3, popadanec: 4 },
        genre_categories: { 'cat:sf': 5 },
        langs: { ru: 1200, en: 7 },
        src_langs: { en: 9 },
        adaptations: 42,
      }),
    });
  });
  await page.route(/\/api\/languages(\?|$)/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [
          { code: 'ru', display: 'Русский', book_count: 10 },
          { code: 'en', display: 'Английский', book_count: 5 },
        ],
      }),
    }),
  );
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto('/authors');
  const sidebar = page.getByRole('complementary', { name: 'Фильтры' });
  await expect(sidebar.getByText('Кинг Стивен')).toHaveCount(0);
  await expect(sidebar.getByText('С экранизациями').locator('..')).toContainText('42', { timeout: 10_000 });
  await expect(sidebar.getByText('Фантастика').locator('..')).toContainText('5');
  await expect(sidebar.getByText('Русский').first().locator('..')).toContainText('1 200');

  await sidebar.getByText('Английский').first().click();
  await expect.poll(() => facetRequests.some((u) => u.includes('langs=en'))).toBe(true);
});
