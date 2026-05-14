import { test, expect } from './_fixtures';

test('book detail: cover image renders when cover_path is set', async ({
  mockedPage: page,
}) => {
  await page.goto('/books/19');
  const cover = page.getByRole('img', { name: /Обложка/ });
  await expect(cover).toBeVisible({ timeout: 10_000 });
  await expect(cover).toHaveAttribute('src', '/api/covers/abc123.jpg');
});
