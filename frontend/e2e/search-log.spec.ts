import { test, expect } from './_fixtures';

// Лог поиска (план recsys/01, 1.4): клик по результату помечает строку лога;
// страница «Поиск» в профиле показывает историю, удаляет строку и всю историю.

test('books: переход к книге из выдачи шлёт клик в лог поиска', async ({ mockedPage: page }) => {
  let clickBody: unknown;
  await page.route(/\/api\/books\?/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [
          { id: 5, work_id: 1005, lib_id: 'L5', title: 'Книга из выдачи', authors: [{ id: 1, name: 'Автор' }], genres: [] },
        ],
        total: 1,
        limit: 20,
        offset: 0,
        query: 'книга',
        search_log_id: 77,
        processing_ms: 1,
      }),
    }),
  );
  await page.route(/\/api\/me\/search-log\/77\/click$/, (route) => {
    clickBody = route.request().postDataJSON();
    void route.fulfill({ status: 204, body: '' });
  });
  await page.route(/\/api\/works\/1005$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ id: 5, work_id: 1005, lib_id: 'L5', title: 'Книга из выдачи', authors: [], genres: [], archive: 'a', file_name: 'f', ext: 'fb2', size_bytes: 1, editions: [] }),
    }),
  );

  await page.goto('/books?q=книга');
  await page.getByRole('link', { name: 'Книга из выдачи' }).click();
  await expect(page).toHaveURL(/\/works\/1005$/);
  await expect.poll(() => clickBody).toEqual({ work_id: 1005 });
});

test('profile: история поиска — список, удаление строки и всей истории', async ({ mockedPage: page }) => {
  const log = {
    ref: [
      { id: 1, q: 'пикник на обочине', hits: 0, at: new Date(Date.now() - 86_400_000).toISOString() },
      { id: 2, q: 'мастер и маргарита', hits: 12, clicked_work_id: 9, clicked_title: 'Мастер и Маргарита', at: new Date().toISOString() },
    ],
  };
  await page.route(/\/api\/me\/search-log\?/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ items: log.ref, total: log.ref.length, retention_days: 365 }),
    }),
  );
  const deleted: string[] = [];
  await page.route(/\/api\/me\/search-log(\/\d+)?$/, (route) => {
    if (route.request().method() !== 'DELETE') return route.fallback();
    const m = route.request().url().match(/search-log\/(\d+)$/);
    deleted.push(m ? m[1] : 'all');
    log.ref = m ? log.ref.filter((e) => e.id !== Number(m[1])) : [];
    return route.fulfill({ status: 204, body: '' });
  });

  await page.goto('/me/searches');
  await expect(page.getByRole('heading', { name: 'Поиск' })).toBeVisible({ timeout: 10_000 });
  const rows = page.getByTestId('search-log').getByRole('listitem');
  await expect(rows).toHaveCount(2);
  await expect(page.getByText('не нашлось', { exact: false })).toBeVisible();
  await expect(page.getByText('открыли «Мастер и Маргарита»', { exact: false })).toBeVisible();
  await expect(page.getByText('хранится 365 дней', { exact: false })).toBeVisible();

  await page.getByRole('button', { name: 'Удалить запрос «пикник на обочине»' }).click();
  await expect(rows).toHaveCount(1);
  expect(deleted).toEqual(['1']);

  await page.getByRole('button', { name: 'Очистить историю' }).click();
  await page.getByRole('button', { name: 'Удалить', exact: true }).click();
  await expect(page.getByText('Пока пусто', { exact: false })).toBeVisible();
  expect(deleted).toEqual(['1', 'all']);
});
