import { test, expect } from './_fixtures';
import { authorDetailFixture } from './_fixtures';

test('author page: bio skeleton swaps to text after polling, photo renders', async ({
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

  // Динамический mock: первый ответ без bio/photo, второй с обоими.
  let hits = 0;
  await page.route(/\/api\/authors\/17$/, (route) => {
    hits++;
    const body =
      hits === 1
        ? { ...authorDetailFixture, bio: undefined, photo_path: undefined }
        : {
            ...authorDetailFixture,
            bio: 'Краткая биография автора из Wikipedia.',
            photo_path: 'author-photo.jpg',
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

  await page.goto('/authors/17');
  // Сначала видим заголовок "Биография" + скелетон.
  await expect(page.getByText('Биография', { exact: true })).toBeVisible({ timeout: 5_000 });
  await expect(page.locator('[aria-label="Биография загружается"]')).toBeVisible();

  // Плейсхолдер фото (role=img, aria-label содержит "загружается").
  await expect(
    page.getByRole('img', { name: /Фото.*загружается/ }),
  ).toBeVisible();

  // После polling — реальный текст и img.
  await expect(page.getByText('Краткая биография автора из Wikipedia.')).toBeVisible({
    timeout: 10_000,
  });
  const photo = page.getByRole('img', { name: /Фото:/, exact: false });
  await expect(photo).toBeVisible();
  await expect(photo).toHaveAttribute('src', '/api/covers/author-photo.jpg');
});
