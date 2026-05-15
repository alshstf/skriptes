import { test, expect } from './_fixtures';
import { bookDetailFixture } from './_fixtures';

test('book detail: shows "Описание отсутствует" after polling exhausts', async ({
  page,
}) => {
  await page.route('**/api/auth/me', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        user: {
          id: 1,
          email: 'tester@example.com',
          display_name: 'Tester',
          role: 'admin',
          created_at: '2026-05-10T00:00:00Z',
        },
      }),
    }),
  );

  // Каждый запрос — без annotation и без cover. Polling в useBook
  // сдастся после ~10 ретраев (dataUpdateCount > 10). С refetchInterval=2000
  // дожидаться "по-настоящему" — это ~22 секунды, что превышает дефолт
  // Playwright. Поэтому используем event-based ожидание текста с явным
  // увеличенным таймаутом — pollingTimeout * 11 = 22s + запас.
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ...bookDetailFixture, cover_path: undefined, annotation: undefined }),
    }),
  );
  await page.route(/\/api\/covers\//, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'image/png',
      body: Buffer.from(
        'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=',
        'base64',
      ),
    }),
  );

  await page.goto('/books/19');
  // Сначала скелетон.
  await expect(page.locator('[aria-label="Аннотация загружается"]')).toBeVisible({
    timeout: 5_000,
  });
  // После исчерпания polling появится "Описание отсутствует".
  await expect(page.getByText('Описание отсутствует.')).toBeVisible({ timeout: 30_000 });
});

test('book detail: annotation skeleton swaps to real text after polling', async ({
  page,
}) => {
  await page.route('**/api/auth/me', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        user: {
          id: 1,
          email: 'tester@example.com',
          display_name: 'Tester',
          role: 'admin',
          created_at: '2026-05-10T00:00:00Z',
        },
      }),
    }),
  );

  // Динамический mock: первый ответ без annotation + cover (только заголовок),
  // последующие — уже с annotation. cover тоже нет, чтобы polling работал.
  let hits = 0;
  await page.route(/\/api\/books\/19$/, (route) => {
    hits++;
    const body =
      hits === 1
        ? { ...bookDetailFixture, cover_path: undefined, annotation: undefined }
        : {
            ...bookDetailFixture,
            cover_path: 'late-cover.png',
            annotation: 'Аннотация книги, два параграфа.\n\nЭто второй параграф.',
          };
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(body),
    });
  });
  await page.route(/\/api\/covers\//, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'image/png',
      body: Buffer.from(
        'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=',
        'base64',
      ),
    }),
  );

  await page.goto('/books/19');

  // Сначала видим заголовок "Аннотация" и скелетон с aria-label.
  await expect(page.getByText('Аннотация', { exact: true })).toBeVisible({
    timeout: 5_000,
  });
  const skeleton = page.getByRole('region', { name: /Аннотация загружается/ })
    .or(page.locator('[aria-label="Аннотация загружается"]'));
  await expect(skeleton).toBeVisible();

  // После polling (~2с) текст аннотации должен появиться, скелетон уйти.
  await expect(page.getByText(/Аннотация книги, два параграфа/)).toBeVisible({
    timeout: 10_000,
  });
  await expect(skeleton).not.toBeVisible();
});
