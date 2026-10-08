import { test, expect } from './_fixtures';

/**
 * Переименование серии правкой админа (#379): в заголовке страницы серии —
 * карандаш, правка уходит как override target_kind=series, field=title.
 */
const series = {
  id: 77,
  title: 'Петля [Алексеев]',
  author_id: 1,
  author_name: 'Алексеев Евгений',
  book_count: 0,
  books: [],
};

test('series: админ переименовывает серию правкой', async ({ mockedPage: page }) => {
  let posted: Record<string, unknown> | null = null;
  await page.route(/\/api\/series\/77$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(series) }),
  );
  await page.route(/\/api\/admin\/overrides$/, async (route) => {
    posted = route.request().postDataJSON();
    await route.fulfill({ status: 200, contentType: 'application/json', body: '{"ok":true}' });
  });

  await page.goto('/series/77');
  await expect(page.getByRole('heading', { name: 'Петля [Алексеев]' })).toBeVisible();
  await page.getByRole('button', { name: 'Править', exact: true }).click(); // режим правки (#444)
  await page.getByRole('button', { name: 'Изменить: Название серии' }).click();
  const input = page.getByRole('textbox', { name: 'Название серии' });
  await input.fill('Петля');
  await input.press('Enter');

  await expect.poll(() => posted).not.toBeNull();
  expect(posted).toMatchObject({ target_kind: 'series', target_id: 77, field: 'title', value: { v: 'Петля' } });
});
