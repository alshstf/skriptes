import { test as base, type Page } from '@playwright/test';

/**
 * Стабы /api/* — нужны чтобы e2e-тесты не зависели от поднятого
 * backend / postgres / meilisearch и были детерминированными.
 *
 * mockApi(page) подключается ДО первого app-fetch'а: открывает
 * /me как авторизованного пользователя, отдаёт небольшую коллекцию
 * книг и одну детальную карточку.
 */

export const userFixture = {
  user: {
    id: 1,
    email: 'tester@example.com',
    display_name: 'Tester',
    role: 'admin',
    created_at: '2026-05-10T00:00:00Z',
  },
};

export const bookListFixture = {
  items: [
    {
      id: 19,
      title: 'Кадетский корпус. Книга 2',
      authors: ['Алексеев Евгений Артёмович'],
      series: 'Петля [Алексеев]',
      genres: ['sf_action', 'popadanec', 'network_literature'],
      year: 2023,
      lang: 'ru',
      lib_id: '749080',
    },
  ],
  total: 1,
  limit: 20,
  offset: 0,
  processing_ms: 1,
};

export const bookDetailFixture = {
  id: 19,
  lib_id: '749080',
  title: 'Кадетский корпус. Книга 2',
  authors: [
    {
      id: 17,
      last_name: 'Алексеев',
      first_name: 'Евгений',
      middle_name: 'Артёмович',
      full_name: 'Алексеев Евгений Артёмович',
    },
  ],
  series: { id: 7, title: 'Петля [Алексеев]' },
  ser_no: 2,
  genres: [
    { id: 1, code: 'sf_action', display: 'sf_action' },
    { id: 2, code: 'popadanec', display: 'popadanec' },
    { id: 3, code: 'network_literature', display: 'network_literature' },
  ],
  lang: 'ru',
  date_added: '2023-02-07',
  archive: 'fb2-749080-749080.zip',
  file_name: '749080',
  ext: 'fb2',
  size_bytes: 849047,
  deleted: false,
};

export async function mockApi(page: Page): Promise<void> {
  await page.route('**/api/auth/me', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(userFixture),
    }),
  );
  // Регэкспы вместо glob — '?' в URL это литерал, glob интерпретирует как
  // "один любой символ" в некоторых реализациях; для надёжности уходим в re.
  await page.route(/\/api\/books(\?|$)/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(bookListFixture),
    }),
  );
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(bookDetailFixture),
    }),
  );
}

export const test = base.extend<{ mockedPage: Page }>({
  mockedPage: async ({ page }, use) => {
    await mockApi(page);
    await use(page);
  },
});

export { expect } from '@playwright/test';
