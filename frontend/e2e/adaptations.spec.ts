import { test, expect } from './_fixtures';

// Спецификация AdaptationsSection: проверяем три ключевых состояния.
//
// Полей `kind` намеренно много — каждый рендерит разный badge ("сериал",
// "мини-сериал", "аниме"); "film" по умолчанию badge не показывает чтобы
// не шумел в типичном случае.

test('adaptations: shows "не найдено" fallback when status=done and empty', async ({
  mockedPage: page,
}) => {
  // Дефолтный mock в _fixtures отдаёт пустой done — секция остаётся
  // видна с заголовком и сообщением "Экранизаций не найдено"
  // (параллельно AnnotationBlock и AuthorBio).
  await page.goto('/books/19');
  await expect(page.getByText(/По этой книге снято/)).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText('Экранизаций не найдено.')).toBeVisible();
});

test('adaptations: shows skeleton while enrichment pending', async ({ mockedPage: page }) => {
  await page.route(/\/api\/books\/19\/adaptations$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ items: [], enrichment_status: 'pending' }),
    }),
  );

  await page.goto('/books/19');
  // Заголовок секции виден сразу; внутри — скелетон с aria-label.
  await expect(page.getByText(/По этой книге снято/)).toBeVisible({ timeout: 10_000 });
  await expect(page.locator('[aria-label="Экранизации загружаются"]')).toBeVisible();
});

test('adaptations: renders cards with poster/year/director/kind', async ({
  mockedPage: page,
}) => {
  await page.route(/\/api\/books\/19\/adaptations$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [
          {
            id: 1,
            provider: 'wikidata',
            ext_id: 'Q12345',
            title: 'Война и мир (1956)',
            year: 1956,
            director: 'King Vidor',
            kind: 'film',
            poster_path: 'fake-poster.jpg',
            ext_url: 'https://www.wikidata.org/wiki/Q12345',
          },
          {
            id: 2,
            provider: 'wikidata',
            ext_id: 'Q67890',
            title: 'Война и мир (мини-сериал)',
            year: 2016,
            director: 'Том Харпер',
            kind: 'miniseries',
            ext_url: 'https://www.wikidata.org/wiki/Q67890',
          },
        ],
        enrichment_status: 'done',
      }),
    }),
  );

  await page.goto('/books/19');

  // Заголовок секции с числом.
  await expect(page.getByText(/По этой книге снято/)).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText('(2)')).toBeVisible();

  // Карточка фильма: название, год, режиссёр. Год идёт в отдельном
  // span; getByText('1956') без exact подтягивает ещё и название
  // ("Война и мир (1956)"), поэтому exact:true.
  await expect(page.getByText('Война и мир (1956)')).toBeVisible();
  await expect(page.getByText('1956', { exact: true })).toBeVisible();
  await expect(page.getByText(/реж\. King Vidor/)).toBeVisible();

  // Карточка мини-сериала: badge "мини-сериал" (для !==film кindов он есть).
  await expect(page.getByText('Война и мир (мини-сериал)')).toBeVisible();
  await expect(page.getByText('мини-сериал', { exact: true })).toBeVisible();

  // Постер первой карточки берётся из /api/covers/{poster_path}.
  // _fixtures отдаёт там 1x1 PNG, так что <img> отрендерится без ошибки.
  const posterImg = page.getByAltText('Постер: Война и мир (1956)');
  await expect(posterImg).toBeVisible();
  await expect(posterImg).toHaveAttribute('src', /\/api\/covers\/fake-poster\.jpg/);

  // Карточка с ext_url — ссылка target=_blank.
  const link = page.locator('a[href="https://www.wikidata.org/wiki/Q12345"]');
  await expect(link).toBeVisible();
  await expect(link).toHaveAttribute('target', '_blank');
});
