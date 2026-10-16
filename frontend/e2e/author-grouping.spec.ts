import { test, expect, authorDetailFixture } from './_fixtures';

test('author page: books are grouped under their series + Вне серий section', async ({
  mockedPage: page,
}) => {
  await page.goto('/authors/17');

  // Заголовок секции серии — кликабельный, ведёт на /series/7.
  const seriesLink = page.getByRole('link', { name: 'Петля [Алексеев]' });
  await expect(seriesLink).toBeVisible({ timeout: 10_000 });
  await expect(seriesLink).toHaveAttribute('href', '/series/7');

  // Badge "СЕРИЯ" рядом с названием — явный визуальный маркер
  // вместо иконки.
  await expect(page.getByText('Серия', { exact: true })).toBeVisible();

  // Книги серии — обе видны на странице автора.
  await expect(page.getByText('Кадетский корпус. Книга 1')).toBeVisible();
  await expect(page.getByText('Кадетский корпус. Книга 2')).toBeVisible();

  // Номера томов в серии: aria-label "Том 1" / "Том 2".
  await expect(page.getByLabel('Том 1')).toBeVisible();
  await expect(page.getByLabel('Том 2')).toBeVisible();

  // Псевдосекция "Вне серий" + книга в ней.
  await expect(page.getByText('Вне серий')).toBeVisible();
  await expect(page.getByText('Отдельный роман')).toBeVisible();
});

// #530: с обложкой номер тома — бейджем на её углу, без отдельной колонки:
// нумерованные и ненумерованные тома серии стоят ровно по одной вертикали.
test('серия на карточке автора: номер тома — бейдж на обложке, тома выровнены (375px)', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await page.route(/\/api\/authors\/17$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        ...authorDetailFixture,
        series: [{ id: 7, title: 'Петля [Алексеев]', count: 3 }],
        books: [
          ...authorDetailFixture.books.filter((b) => b.series_id === 7),
          { id: 103, title: 'Том без номера', authors: ['Алексеев Евгений Артёмович'], series: 'Петля [Алексеев]', series_id: 7, lib_id: '749090' },
        ],
      }),
    }),
  );
  await page.goto('/authors/17');
  const rowOf = (title: string) => page.locator('li', { has: page.getByRole('link', { name: title }) });
  await expect(rowOf('Том без номера')).toBeVisible({ timeout: 10_000 });
  const cover = async (title: string) => (await rowOf(title).getByRole('img').first().boundingBox())!;
  const numbered = await cover('Кадетский корпус. Книга 1');
  const plain = await cover('Том без номера');
  expect(Math.abs(numbered.x - plain.x)).toBeLessThanOrEqual(1);
  // Бейдж — на углу обложки, а не колонкой слева от неё.
  const badge = (await rowOf('Кадетский корпус. Книга 1').getByTestId('ser-no-badge').boundingBox())!;
  expect(badge.x).toBeGreaterThan(numbered.x - badge.width);
  expect(badge.x).toBeLessThan(numbered.x + numbered.width / 2);
  expect(badge.y).toBeLessThan(numbered.y + numbered.height / 2);
  await expect(rowOf('Кадетский корпус. Книга 1').getByLabel('Том 1')).toHaveText('1');
});
