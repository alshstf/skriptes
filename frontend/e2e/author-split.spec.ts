import { test, expect, mockApi } from './_fixtures';

// Разделение автора (#356): подсказка «два автора» по эпохам и диалог выбора работ
// — только админу; запрос уходит с уточнением и выбранными work_id.

const bergAuthor = {
  id: 24608,
  last_name: 'Берг',
  first_name: 'Николай',
  full_name: 'Берг Николай',
  book_count: 5,
  books_total: 5,
  books: [
    { id: 1, work_id: 101, title: 'Гладиатор', year: 1850, authors: ['Берг Николай'], lib_id: 'L1' },
    { id: 2, work_id: 102, title: 'Стихотворения', year: 1855, authors: ['Берг Николай'], lib_id: 'L2' },
    { id: 3, work_id: 103, title: 'Ночная смена', year: 2010, authors: ['Берг Николай'], lib_id: 'L3' },
    { id: 4, work_id: 104, title: 'Крепость живых', year: 2011, authors: ['Берг Николай'], lib_id: 'L4' },
    { id: 5, work_id: 105, title: 'Остров живых', year: 2012, authors: ['Берг Николай'], lib_id: 'L5' },
  ],
  era_split: {
    older: { from: 1850, to: 1855, work_ids: [101, 102] },
    newer: { from: 2010, to: 2012, work_ids: [103, 104, 105] },
  },
  is_favorite: false,
  year_stats: [],
  read_count: 0,
  enrichment_fetched: true,
};

test('разделение автора: подсказка, отмеченная старшая эпоха, запрос с уточнением', async ({ page }) => {
  await mockApi(page); // admin по умолчанию
  await page.route(/\/api\/authors\/24608$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(bergAuthor) }),
  );
  const calls: Array<{ note: string; work_ids: number[] }> = [];
  await page.route(/\/api\/admin\/authors\/24608\/split$/, (route) => {
    calls.push(route.request().postDataJSON());
    void route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ author_id: 99 }) });
  });

  await page.goto('/authors/24608');
  await expect(page.getByText(/Похоже, под этим именем два автора/)).toBeVisible({ timeout: 10_000 });
  await page.getByRole('button', { name: 'Разделить автора…' }).click();

  const dialog = page.getByRole('dialog');
  await expect(dialog.getByRole('button', { name: /Гладиатор/ })).toHaveAttribute('aria-pressed', 'true');
  await expect(dialog.getByRole('button', { name: /Ночная смена/ })).toHaveAttribute('aria-pressed', 'false');
  const submit = dialog.getByRole('button', { name: 'Перенести' });
  await expect(submit).toBeDisabled(); // нет уточнения
  await dialog.getByLabel('Уточнение для нового автора').fill('поэт');
  await submit.click();

  await expect.poll(() => calls.length).toBe(1);
  expect(calls[0].note).toBe('поэт');
  expect([...calls[0].work_ids].sort((a, b) => a - b)).toEqual([101, 102]);
  await expect(page).toHaveURL(/\/authors\/99$/);
});

test('разделение автора: не-админ ничего не видит', async ({ page }) => {
  await mockApi(page);
  await page.route(/\/api\/auth\/me$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ user: { id: 1, email: 'u@example.com', display_name: 'U', role: 'user', created_at: '2026-05-10T00:00:00Z' } }),
    }),
  );
  await page.route(/\/api\/authors\/24608$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(bergAuthor) }),
  );
  await page.goto('/authors/24608');
  await expect(page.getByRole('heading', { name: 'Берг Николай' })).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText(/Похоже, под этим именем два автора/)).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Разделить автора…' })).toHaveCount(0);
});
