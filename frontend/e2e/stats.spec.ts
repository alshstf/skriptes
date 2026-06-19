import { test, expect } from './_fixtures';

test('author stats: progress + histogram appear on author page', async ({
  mockedPage: page,
}) => {
  await page.goto('/authors/17');
  await expect(page.getByText('Статистика', { exact: true })).toBeVisible({
    timeout: 10_000,
  });
  // Прогресс: 2 / 5 книг прочитано (read_count теперь = completed_at IS NOT NULL).
  await expect(page.getByText(/Прочитано 2 из 5/)).toBeVisible();
  // Гистограмма — проверяем что внутри блока есть svg графика. Таргетим
  // именно recharts-surface, а не первый попавшийся svg: в хедере теперь
  // живут lucide-иконки (в т.ч. бургер md:hidden), и `svg().first()`
  // цеплялся бы за скрытую иконку меню вместо графика.
  const statsCard = page.locator('div').filter({ hasText: 'Книги по годам написания' }).first();
  await expect(statsCard.locator('svg.recharts-surface').first()).toBeVisible();
});

test('series stats: same block appears on series page', async ({ mockedPage: page }) => {
  await page.goto('/series/7');
  await expect(page.getByText('Статистика', { exact: true })).toBeVisible({
    timeout: 10_000,
  });
  await expect(page.getByText(/Прочитано 1 из 3/)).toBeVisible();
});
