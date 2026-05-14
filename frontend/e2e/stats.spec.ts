import { test, expect } from './_fixtures';

test('author stats: progress + histogram appear on author page', async ({
  mockedPage: page,
}) => {
  await page.goto('/authors/17');
  await expect(page.getByText('Статистика', { exact: true })).toBeVisible({
    timeout: 10_000,
  });
  // Прогресс: 2 / 5 книг скачано
  await expect(page.getByText(/Скачано 2 из 5/)).toBeVisible();
  // Гистограмма — проверяем что внутри блока есть svg (recharts его рендерит).
  const statsCard = page.locator('div').filter({ hasText: 'Добавлено по годам' }).first();
  await expect(statsCard.locator('svg').first()).toBeVisible();
});

test('series stats: same block appears on series page', async ({ mockedPage: page }) => {
  await page.goto('/series/7');
  await expect(page.getByText('Статистика', { exact: true })).toBeVisible({
    timeout: 10_000,
  });
  await expect(page.getByText(/Скачано 1 из 3/)).toBeVisible();
});
