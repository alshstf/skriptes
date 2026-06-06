import { test, expect, mockApi, bookDetailFixture, bookListFixture } from './_fixtures';

// Секция «Издания» — layout-sensitive (несколько строк, выделение открытого),
// поэтому проверяем в Playwright, а не jsdom (см. граблю про CSS layout).

test('book detail: секция «Издания» показывает все издания работы', async ({ page }) => {
  await mockApi(page);
  const multi = {
    ...bookDetailFixture,
    work_id: 500,
    editions: [
      {
        id: 19,
        lang: 'ru',
        translator: 'Вебер Виктор',
        edition_year: 2015,
        publisher: 'АСТ',
        size_bytes: 849047,
        ext: 'fb2',
        archive: 'a.zip',
        file_name: '749080',
      },
      {
        id: 20,
        lang: 'en',
        edition_year: 1986,
        publisher: 'Viking',
        size_bytes: 700000,
        ext: 'fb2',
        archive: 'b.zip',
        file_name: '749081',
      },
    ],
  };
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(multi) }),
  );

  await page.goto('/books/19');

  await expect(page.getByRole('heading', { name: 'Издания' })).toBeVisible();
  // Атрибуты обоих изданий.
  await expect(page.getByText('Вебер Виктор')).toBeVisible();
  await expect(page.getByText('АСТ')).toBeVisible();
  await expect(page.getByText('Viking')).toBeVisible();
  // На каждое издание своя ссылка «Читать».
  await expect(page.locator('a[href="/books/19/read"]')).toHaveCount(1);
  await expect(page.locator('a[href="/books/20/read"]')).toHaveCount(1);
});

test('book list: бейдж «N изданий» при нескольких изданиях', async ({ page }) => {
  await mockApi(page);
  await page.route(/\/api\/books(\?|$)/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        ...bookListFixture,
        items: [{ ...bookListFixture.items[0], edition_count: 3 }],
      }),
    }),
  );

  await page.goto('/books');
  await expect(page.getByText('3 изданий')).toBeVisible();
});
