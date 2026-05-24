import { test, expect } from './_fixtures';

/**
 * GenreChips: жанры на карточке книги в списке умещаются в ОДНУ строку;
 * лишние прячутся за кликабельным «+N» с поповером. Поведение зависит от
 * реального layout (ширины чипсов) — jsdom его не считает, поэтому только
 * e2e. Узкий вьюпорт + книга с кучей жанров → overflow гарантирован.
 */
test.describe('genre chips overflow (375px)', () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test('лишние жанры за +N с поповером; карточка остаётся ссылкой на деталку', async ({
    mockedPage: page,
  }) => {
    // Override /api/books: одна книга с 8 жанрами — на 375px все в строку
    // не влезут.
    await page.route(/\/api\/books(\?|$)/, (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          items: [
            {
              id: 19,
              title: 'Книга с кучей жанров',
              authors: ['Тестовый Автор'],
              genres: [
                'sf_action',
                'popadanec',
                'network_literature',
                'detective',
                'thriller',
                'romance',
                'history',
                'poetry',
              ],
              lib_id: '1',
              cover_path: 'abc123.jpg',
            },
          ],
          total: 1,
          limit: 20,
          offset: 0,
          processing_ms: 1,
          facets: {},
        }),
      }),
    );

    await page.goto('/books');
    await expect(page.getByText('Книга с кучей жанров')).toBeVisible({ timeout: 10_000 });

    // Часть жанров не влезла → есть «+N».
    const plus = page.getByRole('button', { name: /Ещё \d+ жанр/ });
    await expect(plus).toBeVisible();

    // Чипсы в одну строку — высота ряда близка к одному бейджу (нет переноса).
    const row = page.getByTestId('genre-chips');
    const box = await row.boundingBox();
    expect(box).not.toBeNull();
    expect(box!.height).toBeLessThan(32);

    // Клик по «+N» открывает поповер и НЕ навигирует на деталку.
    await plus.click();
    await expect(page).toHaveURL(/\/books$/);
    const popover = page.getByRole('dialog');
    await expect(popover).toBeVisible();
    // В поповере — невлезшие жанры (хотя бы один из хвоста; fallback на код,
    // т.к. его нет в словаре /api/genres).
    await expect(popover.getByText('poetry')).toBeVisible();

    // Закрываем поповер; клик по заголовку ведёт на деталку (вся карточка —
    // ссылка через stretched-link).
    await page.keyboard.press('Escape');
    await expect(popover).toBeHidden();
    await page.getByRole('link', { name: 'Книга с кучей жанров' }).click();
    await expect(page).toHaveURL(/\/books\/19/);
  });

  test('активный фильтр по жанру — совпавший жанр виден, а не спрятан за +N', async ({
    mockedPage: page,
  }) => {
    // 'poetry' — последний из 8 жанров (без фильтра он бы ушёл под «+N»).
    await page.route(/\/api\/books(\?|$)/, (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          items: [
            {
              id: 19,
              title: 'Книга с кучей жанров',
              authors: ['Тестовый Автор'],
              genres: [
                'sf_action',
                'popadanec',
                'network_literature',
                'detective',
                'thriller',
                'romance',
                'history',
                'poetry',
              ],
              lib_id: '1',
              cover_path: 'abc123.jpg',
            },
          ],
          total: 1,
          limit: 20,
          offset: 0,
          processing_ms: 1,
          facets: {},
        }),
      }),
    );

    // Заходим с активным фильтром genres=poetry.
    await page.goto('/books?genres=poetry');
    await expect(page.getByText('Книга с кучей жанров')).toBeVisible({ timeout: 10_000 });

    // 'poetry' переехал в начало → виден как обычный чип в строке (а не в
    // поповере за «+N»). Скрытые жанры в строке не рендерятся вовсе —
    // поэтому видимость 'poetry' в строке = он попал в показанные.
    const row = page.getByTestId('genre-chips');
    await expect(row.getByText('poetry')).toBeVisible();
  });
});
