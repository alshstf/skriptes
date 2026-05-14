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
  facets: {
    genres: { sf_action: 1, popadanec: 1, network_literature: 1 },
    lang: { ru: 1 },
    year: { '2023': 1 },
  },
};

export const bookDetailFixture = {
  id: 19,
  cover_path: 'abc123.jpg',
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

export const suggestFixture = {
  query: 'кад',
  books: [
    {
      id: 19,
      title: 'Кадетский корпус. Книга 2',
      authors: ['Алексеев Евгений Артёмович'],
      series: 'Петля [Алексеев]',
      genres: [],
      year: 2023,
      lang: 'ru',
      lib_id: '749080',
    },
  ],
  authors: [{ id: 17, full_name: 'Кадет Иван', book_count: 4 }],
  series: [{ id: 7, title: 'Кадетство', author_name: 'Иванов И.', book_count: 12 }],
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
  // Маршруты Playwright матчатся в порядке регистрации, поэтому suggest
  // должен быть зарегистрирован РАНЬШЕ общего /api/books.
  await page.route(/\/api\/search\/suggest(\?|$)/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(suggestFixture),
    }),
  );
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
  // Мокаем /api/covers/{name} крошечным PNG — иначе <img> упадёт в
  // onerror и тест не отличит "не рендерили" от "запрос упал".
  await page.route(/\/api\/covers\//, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'image/png',
      // 1x1 transparent PNG
      body: Buffer.from(
        'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=',
        'base64',
      ),
    }),
  );
  await page.route(/\/api\/authors\/17$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(authorDetailFixture),
    }),
  );
  await page.route(/\/api\/series\/7$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(seriesDetailFixture),
    }),
  );
}

// authorDetailFixture/seriesDetailFixture — для AuthorPage и SeriesPage,
// с непустыми year_stats и read_count чтобы проверить блок "Статистика".
export const authorDetailFixture = {
  id: 17,
  last_name: 'Алексеев',
  first_name: 'Евгений',
  middle_name: 'Артёмович',
  full_name: 'Алексеев Евгений Артёмович',
  book_count: 5,
  books_total: 5,
  top_genres: [{ code: 'sf_action', display: 'Боевая фантастика', count: 3 }],
  series: [{ id: 7, title: 'Петля [Алексеев]', count: 2 }],
  books: [
    {
      id: 100,
      title: 'Кадетский корпус. Книга 1',
      authors: ['Алексеев Евгений Артёмович'],
      series: 'Петля [Алексеев]',
      series_id: 7,
      ser_no: 1,
      lib_id: '749079',
    },
    {
      id: 101,
      title: 'Кадетский корпус. Книга 2',
      authors: ['Алексеев Евгений Артёмович'],
      series: 'Петля [Алексеев]',
      series_id: 7,
      ser_no: 2,
      lib_id: '749080',
    },
    {
      id: 102,
      title: 'Отдельный роман',
      authors: ['Алексеев Евгений Артёмович'],
      lib_id: '749088',
    },
  ],
  is_favorite: false,
  year_stats: [
    { year: 2020, count: 2 },
    { year: 2021, count: 1 },
    { year: 2023, count: 2 },
  ],
  read_count: 2,
};

export const seriesDetailFixture = {
  id: 7,
  title: 'Петля [Алексеев]',
  author_id: 17,
  author_name: 'Алексеев Евгений Артёмович',
  book_count: 3,
  books: [],
  is_favorite: false,
  year_stats: [
    { year: 2020, count: 1 },
    { year: 2022, count: 2 },
  ],
  read_count: 1,
};

export const test = base.extend<{ mockedPage: Page }>({
  mockedPage: async ({ page }, use) => {
    await mockApi(page);
    await use(page);
  },
});

export { expect } from '@playwright/test';
