import { test, expect } from './_fixtures';
import { bookDetailFixture } from './_fixtures';

test('book detail: cover image renders when cover_path is set', async ({
  mockedPage: page,
}) => {
  await page.goto('/books/19');
  const cover = page.getByRole('img', { name: /Обложка: Кадетский корпус/ });
  await expect(cover).toBeVisible({ timeout: 10_000 });
  // Когда cover_path присутствует в фикстуре — рендерится тег img.
  await expect(cover).toHaveAttribute('src', '/api/covers/abc123.jpg');
});

test('book detail: placeholder shows immediately, real cover swaps in after polling', async ({
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

  // Динамический mock /api/books/19: первый запрос без cover_path
  // (плейсхолдер), последующие — с (img появляется).
  let hits = 0;
  await page.route(/\/api\/books\/19$/, (route) => {
    hits++;
    const body =
      hits === 1
        ? { ...bookDetailFixture, cover_path: undefined }
        : { ...bookDetailFixture, cover_path: 'late-cover.png' };
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(body),
    });
  });

  await page.goto('/books/19');

  // Сначала видим плейсхолдер (role=img, aria-label содержит "загружается").
  const placeholder = page.getByRole('img', { name: /загружается/ });
  await expect(placeholder).toBeVisible({ timeout: 5_000 });

  // После refetchInterval (2s) реальный img появится без перезагрузки.
  const real = page.getByRole('img', { name: /Обложка: Кадетский корпус/ });
  await expect(real).toBeVisible({ timeout: 10_000 });
  await expect(real).toHaveAttribute('src', '/api/covers/late-cover.png');

  // И плейсхолдер исчезает.
  await expect(placeholder).not.toBeVisible();
});
