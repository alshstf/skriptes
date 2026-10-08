import { test, expect } from './_fixtures';

// Умные полки (#389): на /books фильтры сохраняются как полка; на /shelves —
// блок «Умные полки» с описанием фильтров, счётчиком и книгами по раскрытию.

const shelf = {
  id: 5,
  name: 'Непрочитанная фантастика',
  filters: { genres: ['sf'], lang: 'ru', unread: true },
  created_at: '2026-10-08T10:00:00Z',
  updated_at: '2026-10-08T10:00:00Z',
};

test('/books: фильтр «Только непрочитанные» и «Сохранить как полку»', async ({ mockedPage: page }) => {
  const posted: unknown[] = [];
  const booksURLs: string[] = [];
  await page.route(/\/api\/books\?/, async (route) => {
    booksURLs.push(route.request().url());
    await route.fallback();
  });
  await page.route(/\/api\/me\/smart-shelves$/, async (route) => {
    if (route.request().method() === 'POST') {
      posted.push(route.request().postDataJSON());
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(shelf) });
      return;
    }
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [] }) });
  });
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto('/books?lang=ru');
  await page.getByRole('switch', { name: 'Только непрочитанные' }).click();
  await expect(page).toHaveURL(/unread=true/);
  await expect.poll(() => booksURLs.some((u) => u.includes('unread=1'))).toBe(true);

  await page.getByRole('button', { name: 'Сохранить как полку' }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog.getByRole('textbox', { name: 'Название полки' })).toHaveValue(/непрочитанные/);
  await dialog.getByRole('textbox', { name: 'Название полки' }).fill('Непрочитанное на русском');
  await dialog.getByRole('button', { name: 'Сохранить' }).click();
  await expect.poll(() => posted.length).toBe(1);
  expect(posted[0]).toEqual({ name: 'Непрочитанное на русском', filters: { lang: 'ru', unread: true } });
});

test('/shelves: умная полка — описание, счётчик, книги, ссылка в каталог', async ({ mockedPage: page }) => {
  await page.route(/\/api\/me\/collections$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [] }) }),
  );
  await page.route(/\/api\/me\/presets$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [] }) }),
  );
  await page.route(/\/api\/me\/smart-shelves$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [shelf] }) }),
  );
  await page.route(/\/api\/books\?.*unread=1/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [{ id: 301, title: 'Пикник на обочине', authors: ['Стругацкий Аркадий'], lib_id: '' }],
        total: 42,
        limit: 20,
        offset: 0,
        processing_ms: 1,
      }),
    }),
  );
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/shelves');
  const section = page.getByRole('region', { name: 'Умные полки' });
  await expect(section).toBeVisible({ timeout: 10_000 });
  await expect(section.getByText('непрочитанные')).toBeVisible();
  await expect(section.getByText('42 кн.')).toBeVisible();
  await section.getByRole('button', { name: /^Непрочитанная фантастика/ }).click();
  await expect(section.getByRole('link', { name: /Пикник на обочине/ })).toHaveAttribute('href', '/works/301');
  await expect(section.getByRole('link', { name: /Все 42/ })).toHaveAttribute('href', /\/books\?.*unread=true/);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
});
