import { test, expect, bookDetailFixture } from './_fixtures';

// Полка «Хочу прочитать» (план recsys/03, 3.3): закладка на карточке книги →
// POST/DELETE /api/books/19/to-read; на /shelves служебная полка без «удалить»
// и подсказка о разнице со ★.

const shelf = (id: number, name: string, kind: string, count: number) => ({
  id,
  name,
  kind,
  book_count: count,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
});

test('to-read: закладка на карточке книги', async ({ mockedPage: page }) => {
  let isToRead = false;
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ...bookDetailFixture, is_to_read: isToRead }),
    }),
  );
  const calls: string[] = [];
  await page.route(/\/api\/books\/19\/to-read$/, (route) => {
    calls.push(route.request().method());
    isToRead = route.request().method() === 'POST';
    void route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ is_to_read: isToRead }) });
  });
  await page.goto('/books/19');
  const add = page.locator('button[aria-label="Добавить в «Хочу прочитать»"]:visible').first();
  await expect(add).toBeVisible({ timeout: 10_000 });
  await add.click();
  await expect.poll(() => calls).toEqual(['POST']);
  await expect(page.locator('button[aria-label="Убрать из «Хочу прочитать»"]:visible').first()).toBeVisible();
});

test('to-read: служебная полка на /shelves', async ({ mockedPage: page }) => {
  await page.route(/\/api\/me\/collections$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [shelf(1, 'Избранное', 'favorites', 2), shelf(2, 'Хочу прочитать', 'to_read', 1), shelf(3, 'Моя', 'user', 0)],
      }),
    }),
  );
  await page.route(/\/api\/me\/presets$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '{"items":[]}' }),
  );
  await page.route(/\/api\/me\/collections\/2$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [{ id: 19, work_id: 1019, title: 'Отложенная книга', authors: ['Автор Отложенный'], added_at: '2026-08-01T00:00:00Z' }],
      }),
    }),
  );
  await page.goto('/shelves');
  await expect(page.getByText('★ Избранное — книги, которые любите')).toBeVisible({ timeout: 10_000 });
  await expect(page.getByRole('button', { name: 'Удалить полку «Хочу прочитать»' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Удалить полку «Избранное»' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Удалить полку «Моя»' })).toBeVisible();

  // Раскрытая полка: строка книги с миниатюрой обложки (издание на полке).
  const row = page.getByRole('listitem').filter({ hasText: 'Хочу прочитать' }).first();
  await row.getByRole('button', { name: 'Раскрыть полку' }).click();
  await expect(page.getByRole('link', { name: /Отложенная книга/ })).toHaveAttribute('href', '/works/1019');
  await expect(page.getByRole('img', { name: 'Обложка: Отложенная книга' })).toBeVisible();
});
