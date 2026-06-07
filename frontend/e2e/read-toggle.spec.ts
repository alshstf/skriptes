import { test, expect } from './_fixtures';
import { bookDetailFixture } from './_fixtures';

// Статус «Прочитана» теперь живёт в meta-блоке карточки книги (не
// в action-ряде). Тесты проверяют:
//  1. is_read=false → видим «Нет ·» + кнопка «Отметить» → клик → POST /read
//  2. is_read=true, есть read_at → видим дату + кнопка «снять» → клик → DELETE /read
//  3. кнопка «Читать» в action-ряде ведёт на /books/:id/read
//  4. с reading_fraction>0 кнопка показывает «Продолжить N%»

test('read status: «Нет» → «Отметить» click marks as read', async ({ mockedPage: page }) => {
  // Состояние is_read мутирует через /read и должно отражаться в
  // последующих GET /api/books/19 (после useToggleRead.onSettled
  // инвалидирует book-кэш — нужен консистентный ответ).
  let isRead = false;
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        ...bookDetailFixture,
        is_read: isRead,
        read_at: isRead ? '2026-05-17T20:00:00Z' : undefined,
      }),
    }),
  );
  const calls: { method: string }[] = [];
  await page.route(/\/api\/books\/19\/read$/, (route) => {
    calls.push({ method: route.request().method() });
    isRead = route.request().method() === 'POST';
    void route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ is_read: isRead }),
    });
  });

  await page.goto('/books/19');
  await expect(page.getByRole('term').filter({ hasText: /^Прочитана$/ })).toBeVisible({
    timeout: 10_000,
  });
  const markBtn = page.getByRole('button', { name: /Отметить/ });
  await expect(markBtn).toBeVisible();
  await markBtn.click();

  // После клика и refetch — должна остаться кнопка «снять» (а не
  // вернуться обратно в «Отметить» от stale-кэша).
  await expect(page.getByRole('button', { name: /Снять отметку «прочитано»/ })).toBeVisible();
  await expect.poll(() => calls.length).toBe(1);
  expect(calls[0].method).toBe('POST');
});

test('read status: показывает дату прочтения + кнопку «снять»', async ({ mockedPage: page }) => {
  let isRead = true;
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        ...bookDetailFixture,
        is_read: isRead,
        read_at: isRead ? '2026-05-17T10:30:00Z' : undefined,
      }),
    }),
  );
  const calls: { method: string }[] = [];
  await page.route(/\/api\/books\/19\/read$/, (route) => {
    calls.push({ method: route.request().method() });
    isRead = route.request().method() === 'POST';
    void route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ is_read: isRead }),
    });
  });

  await page.goto('/books/19');
  // Должна быть видна дата в человеческом формате (русская локаль).
  await expect(page.getByText(/17 мая 2026/)).toBeVisible({ timeout: 10_000 });

  const unmarkBtn = page.getByRole('button', { name: /Снять отметку «прочитано»/ });
  await expect(unmarkBtn).toBeVisible();
  await unmarkBtn.click();

  await expect(page.getByRole('button', { name: /Отметить/ })).toBeVisible();
  await expect.poll(() => calls.length).toBe(1);
  expect(calls[0].method).toBe('DELETE');
});

test('read button: «Читать» без прогресса', async ({ mockedPage: page }) => {
  await page.goto('/books/19');
  const link = page.getByRole('link', { name: /Открыть книгу в браузерном ридере/ });
  await expect(link).toBeVisible({ timeout: 10_000 });
  // Без reading_fraction видим базовый лейбл «Читать».
  await expect(link).toContainText('Читать');
});

test('read button: «Продолжить N%» при reading_fraction', async ({ mockedPage: page }) => {
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ...bookDetailFixture, reading_fraction: 0.37 }),
    }),
  );

  await page.goto('/books/19');
  const link = page.getByRole('link', { name: /Открыть книгу в браузерном ридере/ });
  await expect(link).toBeVisible({ timeout: 10_000 });
  await expect(link).toContainText('Продолжить 37%');
});

test('reader: «Читать» link navigates to /books/19/read', async ({ mockedPage: page }) => {
  // foliate-reader.html в e2e не прогнать без настоящего epub — проверяем
  // только что link ведёт на /read и страница ридера загружается.
  await page.route(/\/api\/books\/19\/position$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ pos: '' }),
    }),
  );

  await page.goto('/books/19');
  const link = page.getByRole('link', { name: /Открыть книгу в браузерном ридере/ });
  await expect(link).toBeVisible({ timeout: 10_000 });
  await link.click();
  await expect(page).toHaveURL(/\/books\/19\/read$/, { timeout: 10_000 });
  await expect(page.getByRole('button', { name: /Вернуться к карточке книги/ })).toBeVisible();
});

test('reader: «К карточке» ведёт на карточку и browser-back не возвращает в ридер', async ({
  mockedPage: page,
}) => {
  // «К карточке» делает navigate(replace) на карточку. window.history.back()
  // здесь ненадёжен: foliate в iframe плодит свои history-записи, и back мог
  // увести в ридер другого издания (баг, который заметил юзер). Проверяем:
  // (1) «К карточке» → карточка; (2) browser-back НЕ возвращает в ридер.
  await page.route(/\/api\/books\/19\/position$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ pos: '' }),
    }),
  );

  await page.goto('/books');
  // Список ведёт на карточку работы /works/19.
  await page.getByRole('link', { name: /Кадетский корпус. Книга 2/ }).first().click();
  await expect(page).toHaveURL(/\/works\/19$/);
  await page.getByRole('link', { name: /Открыть книгу в браузерном ридере/ }).click();
  await expect(page).toHaveURL(/\/books\/19\/read$/);
  // «К карточке» из ридера ведёт на /books/{editionId} (back-compat → та же
  // карточка работы), ему не нужен work_id.
  await page.getByRole('button', { name: /Вернуться к карточке книги/ }).click();
  await expect(page).toHaveURL(/\/books\/19$/);
  // Browser-back НЕ должен вернуть в ридер (replace убрал reader-запись).
  await page.goBack();
  await expect(page).not.toHaveURL(/\/books\/19\/read$/);
});
