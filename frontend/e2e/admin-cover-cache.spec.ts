import { test, expect } from './_fixtures';

/**
 * Раздел «Администрирование» → подраздел «Кэш обложек»: панель грузит
 * настройки + статистику, есть таб-навигация. mockedPage — админ.
 */
test('admin: панель кэша обложек грузит настройки и статистику', async ({ mockedPage: page }) => {
  await page.route(/\/api\/admin\/cover-cache$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        cache_max_mb: 8192,
        cache_min_free_mb: 1024,
        prewarm: false,
        cache_size_bytes: 1048576, // 1 МБ
        free_bytes: 5368709120, // 5 ГБ
      }),
    }),
  );

  await page.goto('/admin/cover-cache');
  await expect(page.getByRole('heading', { name: 'Кэш обложек' })).toBeVisible({ timeout: 10_000 });

  // Форма заполнена из настроек.
  await expect(page.getByLabel('Бюджет кэша, МБ')).toHaveValue('8192');
  await expect(page.getByLabel('Порог свободного места, МБ')).toHaveValue('1024');
  // Статистика отрисована.
  await expect(page.getByText('1.0 МБ')).toBeVisible();
  await expect(page.getByText('5.0 ГБ')).toBeVisible();

  // Таб-навигация раздела присутствует.
  await expect(page.getByRole('link', { name: 'Пользователи' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Кэш обложек' })).toBeVisible();
});
