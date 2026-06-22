import { test, expect } from './_fixtures';

/**
 * «Администрирование» → «Фоновые операции»: страница организована аккордеоном по
 * типам данных (обложки/аннотации/год/био+фото/экранизации) с режимом
 * Выкл/Лениво/Фоном. Проверяем загрузку настроек, производные режимы, покрытие и
 * таб-навигацию. mockedPage — админ.
 */
test('admin: панель фоновых операций — аккордеон по типам', async ({ mockedPage: page }) => {
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
        coverage: { total: 100, with_year: 41, by_source: { fb2_title: 30, openlibrary: 11 } },
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
  await page.route(/\/api\/admin\/external-rating$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        enabled: false,
        googlebooks: true,
        openlibrary: true,
        whole_collection: false,
        googlebooks_rpm: 60,
        openlibrary_rpm: 60,
        not_found_retry_days: 90,
        error_retry_hours: 24,
        external_rating_running: false,
        external_rating_mode: 'off',
        coverage: { total: 10, with_rating: 6, with_web: 1, by_source: { googlebooks: 1 } },
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
  await page.route(/\/api\/admin\/enrichment-gates$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        cover_disabled: false,
        annotation_disabled: false,
        author_disabled: false,
        adaptation_disabled: false,
      }),
    }),
  );

  await page.goto('/admin/background');
  await expect(page.getByRole('heading', { name: 'Фоновые операции' })).toBeVisible({ timeout: 10_000 });

  // «Общие»: порог свободного места + свободно на диске.
  await expect(page.getByLabel('Порог свободного места, МБ')).toHaveValue('1024');
  await expect(page.getByText('5.0 ГБ')).toBeVisible();

  // Аккордеон по типам: все пять заголовков.
  await expect(page.getByText('Обложки', { exact: true })).toBeVisible();
  await expect(page.getByText('Аннотации', { exact: true })).toBeVisible();
  await expect(page.getByText('Год написания', { exact: true })).toBeVisible();
  await expect(page.getByText('Биографии и фото авторов', { exact: true })).toBeVisible();
  await expect(page.getByText('Экранизации', { exact: true })).toBeVisible();

  // Производные режимы: обложки — Лениво (фон выключен), год — Выкл.
  await expect(page.getByTestId('cover-mode-lazy')).toHaveAttribute('aria-pressed', 'true');
  await expect(page.getByTestId('year-mode-off')).toHaveAttribute('aria-pressed', 'true');

  // Покрытия в свёрнутых заголовках.
  await expect(page.getByText('обложка у 70%')).toBeVisible();
  await expect(page.getByText('год у 41%')).toBeVisible();

  // Таб-навигация админки.
  await expect(page.getByRole('link', { name: 'Пользователи' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Фоновые операции' })).toBeVisible();
});
