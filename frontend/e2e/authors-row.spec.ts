import { test, expect } from './_fixtures';

// Строка автора в /authors на телефоне (#579): один жанр и «+N», значок премий,
// сводка без висячих «·», годы жизни вместо голого диапазона лет работ.

const author = {
  id: 2121,
  full_name: 'Булгаков Михаил Афанасьевич',
  book_count: 73,
  is_favorite: true,
  favorited_books_count: 0,
  has_adaptations: true,
  has_awards: true,
  born_year: 1891,
  died_year: 1940,
  years_active: { from: 1919, to: 1940 },
  external_rating: 4.5,
  external_rating_source: 'top_works',
  reader_rating: 4.8,
  reader_rating_count: 12,
  top_genres: [
    { code: 'prose_classic', display: 'Классическая проза', count: 40 },
    { code: 'dramaturgy', display: 'Драматургия', count: 12 },
    { code: 'sf_social', display: 'Социальная фантастика', count: 5 },
    { code: 'prose_rus_classic', display: 'Русская классика', count: 4 },
    { code: 'nonf_biography', display: 'Биографии', count: 2 },
  ],
  languages: ['ru', 'en'],
};

test('авторы: строка на мобиле — один жанр и «+N», премии, сводка без разделителей', async ({
  mockedPage: page,
}) => {
  await page.route(/\/api\/authors(\?|$)/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ items: [author, { ...author, id: 7, full_name: 'Автор Безгодов', born_year: undefined, died_year: undefined, has_awards: false, years_active: { from: 1974, to: 2009 } }], total: 2 }),
    }),
  );
  await page.setViewportSize({ width: 375, height: 812 });
  await page.goto('/authors');
  const row = page.locator('div.relative', { has: page.getByRole('link', { name: 'Булгаков Михаил Афанасьевич' }) }).first();
  await expect(row).toBeVisible({ timeout: 10_000 });

  // Жанры: один чип и «+4», поповер со скрытыми — без перехода на карточку.
  const chips = row.getByTestId('genre-chips');
  await expect(chips.getByText('Классическая проза')).toBeVisible();
  await expect(chips.getByText('Драматургия')).toHaveCount(0);
  await chips.getByRole('button', { name: 'Ещё 4 жанр(ов)' }).click();
  await expect(page.getByRole('dialog').getByText('Драматургия')).toBeVisible();
  await expect(page).toHaveURL(/\/authors$/);
  await page.keyboard.press('Escape');

  // Значок премий рядом с экранизациями; годы — годы жизни.
  await expect(row.getByLabel('Лауреат премий')).toBeVisible();
  await expect(row.getByLabel('Есть экранизации')).toBeVisible();
  await expect(row.getByText('1891–1940', { exact: true })).toBeVisible();
  // Сводка без текстовых «·»: перенос не оставляет висячего разделителя.
  const summary = row.locator('p', { hasText: 'в каталоге' });
  await expect(summary).not.toContainText('·');
  // Рейтинг — в одной строке с числом книг (сразу после него).
  const books = await summary.getByText(/73 книги в каталоге/).boundingBox();
  const rating = await summary.getByLabel(/Внешний рейтинг 4,5|Внешний рейтинг 4.5/).boundingBox();
  expect(Math.abs(books!.y - rating!.y)).toBeLessThan(4);

  // Без годов жизни — подписанный диапазон лет работ.
  await expect(page.getByText('работы 1974–2009', { exact: true })).toBeVisible();

  // Клик по строке (мимо «+N») ведёт на карточку автора: растянутая ссылка
  // имени перекрывает строку.
  const b = (await summary.getByText(/73 книги в каталоге/).boundingBox())!;
  await page.mouse.click(b.x + 10, b.y + b.height / 2);
  await expect(page).toHaveURL(/\/authors\/2121$/);
});

test('авторы: на десктопе жанров — сколько влезает в строку', async ({ mockedPage: page }) => {
  await page.route(/\/api\/authors(\?|$)/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [author], total: 1 }) }),
  );
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto('/authors');
  const chips = page.getByTestId('genre-chips').first();
  await expect(chips.getByText('Драматургия')).toBeVisible({ timeout: 10_000 });
});
