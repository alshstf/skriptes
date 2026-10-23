import { test, expect } from './_fixtures';

// Фильтр по названию у раскрытой полки /shelves (#582): появляется на длинных
// полках, фильтрует загруженный список без запроса к серверу, закреплён под
// шапкой при скролле; у умной полки — ищет по всей полке через /api/books.

const shelf = (id: number, name: string, kind: string, count: number) => ({
  id,
  name,
  kind,
  book_count: count,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
});

const longShelf = Array.from({ length: 30 }, (_, i) => ({
  id: 100 + i,
  work_id: 1100 + i,
  title: i === 17 ? 'Скотный двор' : `Книга номер ${i + 1}`,
  authors: [i === 17 ? 'Оруэлл Джордж' : `Автор ${i + 1}`],
  added_at: '2026-08-01T00:00:00Z',
}));

async function mockShelves(page: import('@playwright/test').Page) {
  await page.route(/\/api\/me\/collections$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [shelf(1, 'Избранное', 'favorites', 30), shelf(2, 'Короткая', 'user', 3)],
      }),
    }),
  );
  await page.route(/\/api\/me\/presets$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '{"items":[]}' }),
  );
  await page.route(/\/api\/me\/collections\/1$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: longShelf }) }),
  );
  await page.route(/\/api\/me\/collections\/2$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ items: longShelf.slice(0, 3) }),
    }),
  );
}

test('полка: фильтр по названию и автору, ничего не нашлось, очистка', async ({ mockedPage: page }) => {
  await mockShelves(page);
  await page.setViewportSize({ width: 375, height: 812 });
  await page.goto('/shelves?open=c%3A1&open=c%3A2');
  const filters = page.getByRole('searchbox', { name: 'Фильтр по названию' });
  // Длинная полка — поле есть, короткая (3 книги) — нет.
  await expect(filters).toHaveCount(1, { timeout: 10_000 });
  const fav = page.getByRole('listitem').filter({ has: page.getByText('Избранное', { exact: true }) });
  await expect(fav.getByRole('link', { name: /Книга номер/ })).toHaveCount(29);

  await filters.fill('оруэлл');
  await expect(fav.getByRole('link')).toHaveCount(1);
  await expect(fav.getByRole('link', { name: /Скотный двор/ })).toBeVisible();
  await expect(fav.getByText('Найдено 1 из 30')).toBeVisible();

  await filters.fill('дюна');
  await expect(fav.getByRole('link')).toHaveCount(0);
  await expect(fav.getByText('Ничего не нашлось')).toBeVisible();

  await fav.getByRole('button', { name: 'Очистить фильтр' }).click();
  await expect(filters).toHaveValue('');
  await expect(fav.getByRole('link', { name: /Книга номер/ })).toHaveCount(29);
});

test('полка: поле фильтра закреплено под шапкой при скролле (мобила)', async ({ mockedPage: page }) => {
  await mockShelves(page);
  await page.setViewportSize({ width: 375, height: 812 });
  await page.goto('/shelves?open=c%3A1');
  const bar = page.getByTestId('shelf-filter');
  await expect(bar).toBeVisible({ timeout: 10_000 });
  const header = page.locator('header').first();
  const headerBottom = (await header.boundingBox())!.y + (await header.boundingBox())!.height;

  // Прокрутили к середине полки — поле стоит сразу под шапкой и видно целиком.
  await page.getByRole('link', { name: /Книга номер 20\b/ }).scrollIntoViewIfNeeded();
  await page.mouse.wheel(0, 400);
  await expect
    .poll(async () => Math.round((await bar.boundingBox())!.y))
    .toBeLessThanOrEqual(Math.round(headerBottom));
  const box = (await bar.boundingBox())!;
  expect(box.y).toBeGreaterThanOrEqual(headerBottom - 2);
  // Прилипшее поле отделено тенью, как шапка.
  await expect.poll(() => bar.evaluate((el) => getComputedStyle(el).boxShadow)).toContain('22px');
  // Ввод в закреплённое поле работает.
  await page.getByRole('searchbox', { name: 'Фильтр по названию' }).fill('скотный');
  await expect(page.getByRole('link', { name: /Скотный двор/ })).toBeVisible();
});

test('подборка: фильтр по загруженному списку', async ({ mockedPage: page }) => {
  await page.route(/\/api\/me\/collections$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '{"items":[]}' }),
  );
  await page.route(/\/api\/me\/presets$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [{ key: 'adaptations', title: 'Экранизации 2026–2027', hint: 'Скоро на экранах', count: 10 }],
      }),
    }),
  );
  await page.route(/\/api\/me\/presets\/adaptations$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: longShelf.slice(10, 20).map((b) => ({ ...b, id: b.work_id, lib_id: '' })),
        notes: { '1117': '2026 · «Скотный двор»' },
      }),
    }),
  );
  await page.goto('/shelves?open=p%3Aadaptations');
  const section = page.getByRole('region', { name: 'Подборки' });
  const input = section.getByRole('searchbox', { name: 'Фильтр по названию' });
  await expect(input).toBeVisible({ timeout: 10_000 });
  await input.fill('скотный');
  await expect(section.getByRole('link')).toHaveCount(1);
  await expect(section.getByText('2026 · «Скотный двор»')).toBeVisible();
});

test('умная полка: фильтр ищет по всей полке через поиск', async ({ mockedPage: page }) => {
  const urls: string[] = [];
  await page.route(/\/api\/me\/collections$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '{"items":[]}' }),
  );
  await page.route(/\/api\/me\/presets$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '{"items":[]}' }),
  );
  await page.route(/\/api\/me\/smart-shelves$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [
          {
            id: 5,
            name: 'Фантастика',
            filters: { genres: ['sf'] },
            created_at: '2026-10-08T10:00:00Z',
            updated_at: '2026-10-08T10:00:00Z',
          },
        ],
      }),
    }),
  );
  await page.route(/\/api\/books\?/, (route) => {
    const url = new URL(route.request().url());
    urls.push(url.search);
    const q = url.searchParams.get('q') ?? '';
    const items = q
      ? [{ id: 777, title: 'Пикник на обочине', authors: ['Стругацкий Аркадий'], lib_id: '' }]
      : longShelf.slice(0, 20).map((b) => ({ ...b, lib_id: '' }));
    void route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ items, total: q ? 1 : 420, limit: 20, offset: 0, processing_ms: 1 }),
    });
  });
  await page.goto('/shelves?open=s%3A5');
  const section = page.getByRole('region', { name: 'Умные полки' });
  const input = section.getByRole('searchbox', { name: 'Фильтр по названию' });
  await expect(input).toBeVisible({ timeout: 10_000 });
  await input.fill('пикник');
  await expect(section.getByRole('link', { name: /Пикник на обочине/ })).toBeVisible();
  await expect(section.getByText('Найдено 1 из 420')).toBeVisible();
  expect(urls.some((u) => u.includes('q=%D0%BF%D0%B8%D0%BA%D0%BD%D0%B8%D0%BA') && u.includes('genres='))).toBe(true);
  // Ссылка в каталог несёт и фильтры полки, и текст фильтра.
  await expect(section.getByRole('link', { name: 'Открыть в каталоге' })).toHaveAttribute('href', /q=/);
});
