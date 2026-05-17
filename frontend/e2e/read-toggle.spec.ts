import { test, expect } from './_fixtures';
import { bookDetailFixture } from './_fixtures';

// ReadToggle на BookDetailPage — три сценария:
//  1. is_read=false → видим «Прочитать»; клик → POST /read → меняется на «Прочитано»
//  2. is_read=true (книга уже прочитана) → видим «Прочитано» с галочкой
//  3. клик в «Прочитано»-состоянии → DELETE /read → состояние сменяется обратно
//
// Стабим /api/books/19 с is_read=false (по умолчанию), на /read endpoint
// возвращаем 200; затем подсчитываем сколько раз и каким методом дёргали.

test('read toggle: false → true on click', async ({ mockedPage: page }) => {
  let calls: { method: string }[] = [];
  await page.route(/\/api\/books\/19\/read$/, (route) => {
    calls.push({ method: route.request().method() });
    void route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ is_read: route.request().method() === 'POST' }),
    });
  });

  await page.goto('/books/19');
  const btn = page.getByRole('button', { name: /Отметить книгу как прочитанную/ });
  await expect(btn).toBeVisible({ timeout: 10_000 });
  await btn.click();
  // Optimistic update: aria-label сразу меняется на «Снять отметку».
  await expect(page.getByRole('button', { name: /Снять отметку «прочитано»/ })).toBeVisible();
  await expect.poll(() => calls.length).toBe(1);
  expect(calls[0].method).toBe('POST');
});

test('read toggle: true → false on click (unmark)', async ({ mockedPage: page }) => {
  // Подменяем фикстуру: книга уже прочитана.
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ...bookDetailFixture, is_read: true }),
    }),
  );
  let calls: { method: string }[] = [];
  await page.route(/\/api\/books\/19\/read$/, (route) => {
    calls.push({ method: route.request().method() });
    void route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ is_read: route.request().method() === 'POST' }),
    });
  });

  await page.goto('/books/19');
  const btn = page.getByRole('button', { name: /Снять отметку «прочитано»/ });
  await expect(btn).toBeVisible({ timeout: 10_000 });
  await btn.click();
  // После unmark возвращаемся к исходному «Прочитать».
  await expect(page.getByRole('button', { name: /Отметить книгу как прочитанную/ })).toBeVisible();
  await expect.poll(() => calls.length).toBe(1);
  expect(calls[0].method).toBe('DELETE');
});

test('reader: «Читать» link navigates to /books/19/read', async ({ mockedPage: page }) => {
  // foliate-reader.html нам сложно прогнать в e2e без backend (нужен
  // настоящий epub). Поэтому проверяем только сам facт перехода —
  // что кнопка «Читать» ведёт на правильный роут.
  //
  // Стабим /position и /epub чтобы ReaderPage не зависал в loading.
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
  // На ридер-странице вверху — кнопка «К карточке».
  await expect(page.getByRole('button', { name: /Вернуться к карточке книги/ })).toBeVisible();
});
