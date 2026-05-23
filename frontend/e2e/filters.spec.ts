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

  // Теперь жанры сгруппированы по категориям и свёрнуты по дефолту.
  // Раскрываем «Фантастика», находим leaf «Боевая фантастика» (display
  // для sf_action из фикстуры) и кликаем по нему.
  const fantasyRow = page.locator('div').filter({ hasText: /^Фантастика/ }).first();
  await fantasyRow.getByRole('button', { name: 'Развернуть' }).click();

  const leafCheckbox = page.getByRole('checkbox', { name: /Боевая фантастика/ });
  await expect(leafCheckbox).toBeVisible();
  await leafCheckbox.check();

  // URL содержит sf_action.
  await expect(page).toHaveURL(/sf_action/);

  // Должен прилететь новый запрос с genres=sf_action.
  await expect.poll(() => calls.some((c) => c.includes('genres=sf_action'))).toBe(true);

  // Active-чип использует display-имя из useGenres, а не сырой код.
  await expect(page.getByText(/Жанр: Боевая фантастика/)).toBeVisible();
});

test('filters: tri-state — клик по checkbox категории выбирает все leaf жанры', async ({
  mockedPage: page,
}) => {
  await page.goto('/books');
  await expect(page.getByRole('heading', { name: 'Фильтры' })).toBeVisible({
    timeout: 10_000,
  });

  // Tri-state checkbox у «Фантастика» — выбираем все её жанры одним кликом.
  const fantasyRow = page.locator('div').filter({ hasText: /^Фантастика/ }).first();
  const selectAll = fantasyRow.locator('input[type="checkbox"]').first();
  await selectAll.check();

  // URL содержит оба leaf-кода из категории «Фантастика»: sf_action + popadanec.
  await expect(page).toHaveURL(/sf_action/);
  await expect(page).toHaveURL(/popadanec/);

  // Два чипа активных фильтров с display-именами.
  await expect(page.getByText(/Жанр: Боевая фантастика/)).toBeVisible();
  await expect(page.getByText(/Жанр: Попаданцы/)).toBeVisible();
});

test('filters: sort dropdown changes URL', async ({ mockedPage: page }) => {
  await page.goto('/books');
  await expect(page.getByLabel('Сортировка')).toBeVisible({ timeout: 10_000 });
  await page.getByLabel('Сортировка').selectOption('year_desc');
  await expect(page).toHaveURL(/sort=year_desc/);
});
