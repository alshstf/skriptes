import { test, expect, mockApi, bookDetailFixture } from './_fixtures';

// Ручные merge/split + админ-подсказки. Admin-гейт и поведение завязаны на роль
// и на клиентскую группировку по ser_no — проверяем в Playwright (реальный DOM).

// Серия с дублем тома #7 (две разные работы под одним ser_no) — кейс «Страйка».
const seriesWithDup = {
  id: 7,
  title: 'Корморан Страйк',
  author_id: 17,
  author_name: 'Гэлбрейт Роберт',
  book_count: 2,
  books: [
    {
      id: 14,
      work_id: 269666,
      title: 'Развороченная могила',
      authors: ['Гэлбрейт Роберт'],
      series: 'Корморан Страйк',
      series_id: 7,
      ser_no: 7,
      lib_id: 'L1',
    },
    {
      id: 505134,
      work_id: 277728,
      title: 'Неизбежная могила',
      authors: ['Гэлбрейт Роберт'],
      series: 'Корморан Страйк',
      series_id: 7,
      ser_no: 7,
      lib_id: 'L2',
    },
  ],
  is_favorite: false,
  year_stats: [],
  read_count: 0,
};

function asUser(role: 'admin' | 'user') {
  return JSON.stringify({
    user: {
      id: 1,
      email: 'tester@example.com',
      display_name: 'Tester',
      role,
      created_at: '2026-05-10T00:00:00Z',
    },
  });
}

test('merge suggestion: admin видит плашку и «Объединить» шлёт work_ids', async ({ page }) => {
  await mockApi(page); // userFixture = admin по умолчанию
  await page.route(/\/api\/series\/7$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(seriesWithDup) }),
  );
  const calls: Array<{ work_ids: number[] }> = [];
  await page.route(/\/api\/admin\/works\/merge$/, (route) => {
    calls.push(route.request().postDataJSON());
    void route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ work_id: 269666 }),
    });
  });

  await page.goto('/series/7');
  await expect(page.getByText(/Похоже, том #7/)).toBeVisible({ timeout: 10_000 });
  await page.getByRole('button', { name: 'Объединить', exact: true }).click();

  await expect.poll(() => calls.length).toBe(1);
  expect([...calls[0].work_ids].sort((a, b) => a - b)).toEqual([269666, 277728]);
});

test('merge suggestion: не-админ ничего не видит', async ({ page }) => {
  await mockApi(page);
  // Переопределяем роль ПОСЛЕ mockApi (последний route выигрывает).
  await page.route(/\/api\/auth\/me$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: asUser('user') }),
  );
  await page.route(/\/api\/series\/7$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(seriesWithDup) }),
  );

  await page.goto('/series/7');
  await expect(page.getByText('Развороченная могила')).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText(/Похоже, том #7/)).toHaveCount(0);
  await expect(page.getByRole('button', { name: /Объединить издания/ })).toHaveCount(0);
});

test('split: admin выносит издание в отдельную книгу', async ({ page }) => {
  await mockApi(page);
  const multi = {
    ...bookDetailFixture,
    work_id: 500,
    editions: [
      { id: 19, lang: 'ru', translator: 'Вебер Виктор', size_bytes: 849047, ext: 'fb2', archive: 'a.zip', file_name: '749080' },
      { id: 20, lang: 'en', size_bytes: 700000, ext: 'fb2', archive: 'b.zip', file_name: '749081' },
    ],
  };
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(multi) }),
  );
  const calls: Array<{ book_ids: number[] }> = [];
  await page.route(/\/api\/admin\/works\/split$/, (route) => {
    calls.push(route.request().postDataJSON());
    void route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ work_id: 999 }),
    });
  });

  await page.goto('/books/19');
  await page.getByRole('button', { name: 'Разделить', exact: true }).click();
  // В диалоге выбираем издание #20 (EN · Издание 20) и выносим.
  await page.getByRole('button', { name: /Издание 20/ }).click();
  await page.getByRole('button', { name: /Вынести/ }).click();

  await expect.poll(() => calls.length).toBe(1);
  expect(calls[0].book_ids).toContain(20);
});
