import { test, expect } from './_fixtures';

test('smoke: protected route loads when authenticated', async ({ mockedPage: page }) => {
  await page.goto('/books');
  // Дождаться, что первая книжная карточка появилась — это знак, что
  // /api/auth/me и /api/books отработали и React смонтировал список.
  await expect(page.getByText('Кадетский корпус. Книга 2', { exact: true })).toBeVisible({ timeout: 10_000 });
  // Авторы и серия рендерятся под заголовком — используем как доказательство
  // что отрисовался именно BookListItem, а не где-то ещё (например header).
  await expect(page.getByText('Алексеев Евгений Артёмович')).toBeVisible();
  await expect(page.getByText(/Серия: Петля \[Алексеев\]/)).toBeVisible();
});

test('smoke: redirects to /login when unauthenticated', async ({ page }) => {
  // Этот тест НЕ использует mockedPage: переопределяем /me на 401.
  await page.route('**/api/auth/me', (route) =>
    route.fulfill({
      status: 401,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'not authenticated' }),
    }),
  );
  await page.goto('/books');
  await expect(page).toHaveURL(/\/login$/);
  await expect(page.getByLabel('Email')).toBeVisible();
});
