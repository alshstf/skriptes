import { test, expect } from './_fixtures';

/**
 * Раздел «Администрирование» → «Фоновые операции»: страница грузит настройки
 * обработки коллекции (секция 1) и внешних источников (секция 2) + статистику,
 * есть таб-навигация. mockedPage — админ.
 */
test('admin: панель фоновых операций грузит настройки и статистику', async ({ mockedPage: page }) => {
  await page.route(/\/api\/admin\/cover-cache$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        cache_max_mb: 8192,
        cache_min_free_mb: 1024,
        prewarm: false,
        sync_covers: true,
        sync_annotations: true,
        sync_years: true,
        intensity: 'medium',
        poster_cache_max_mb: 0,
        photo_cache_max_mb: 0,
        prewarm_running: false,
        prewarm_mode: 'off',
        cache_size_bytes: 1048576, // 1 МБ
        poster_cache_size_bytes: 0,
        photo_cache_size_bytes: 0,
        free_bytes: 5368709120, // 5 ГБ
      }),
    }),
  );
  await page.route(/\/api\/admin\/year-enrichment$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        enabled: false,
        openlibrary: true,
        wikidata: true,
        whole_collection: false,
        openlibrary_rpm: 60,
        wikidata_rpm: 20,
        not_found_retry_days: 90,
        error_retry_hours: 24,
        year_backfill_running: false,
        year_backfill_mode: 'off',
        coverage: { total: 0, with_year: 0, by_source: {} },
      }),
    }),
  );
  await page.route(/\/api\/admin\/cover-enrichment$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        enabled: false,
        openlibrary: true,
        googlebooks: true,
        whole_collection: false,
        openlibrary_rpm: 60,
        googlebooks_rpm: 60,
        not_found_retry_days: 90,
        error_retry_hours: 24,
        cover_backfill_running: false,
        cover_backfill_mode: 'off',
        coverage: { total: 10, with_cover: 7, by_source: { openlibrary: 2 } },
      }),
    }),
  );
  await page.route(/\/api\/admin\/bio-adaptation-enrichment$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        bios: false,
        adaptations: false,
        bios_rpm: 30,
        adaptations_rpm: 20,
        bios_running: false,
        bios_mode: 'off',
        adaptations_running: false,
        adaptations_mode: 'off',
        bio_coverage: { total: 8, with_bio: 5, with_photo: 3 },
        adaptation_coverage: { total: 10, with_adaptations: 4 },
      }),
    }),
  );

  await page.goto('/admin/background');
  await expect(page.getByRole('heading', { name: 'Фоновые операции' })).toBeVisible({ timeout: 10_000 });

  // Секция 1: лимиты кэша заполнены, статистика отрисована.
  await expect(page.getByLabel('Бюджет кэша, МБ')).toHaveValue('8192');
  await expect(page.getByLabel('Порог свободного места, МБ')).toHaveValue('1024');
  await expect(page.getByText('1.0 МБ')).toBeVisible();
  await expect(page.getByText('5.0 ГБ')).toBeVisible();

  // Все секции присутствуют + таб-навигация.
  await expect(page.getByText('Обработка коллекции', { exact: true })).toBeVisible();
  await expect(page.getByText('Внешние источники — годы', { exact: true })).toBeVisible();
  await expect(page.getByText('Внешние источники — обложки', { exact: true })).toBeVisible();
  await expect(page.getByText('Внешние источники — биографии и экранизации', { exact: true })).toBeVisible();
  // Покрытие обложек (7/10) + биографий (5/8) из моков.
  await expect(page.getByText('7 из 10 (70%)')).toBeVisible();
  await expect(page.getByText('5 из 8 (63%)')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Пользователи' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Фоновые операции' })).toBeVisible();
});
