import { test, expect, authorDetailFixture } from './_fixtures';

// #505: «Современники» на карточке автора — ссылки на карточки, годы жизни в
// подсказке; некого показать — строки нет.
test('карточка автора: современники — ссылки с годами жизни', async ({ mockedPage: page }) => {
  await page.route(/\/api\/authors\/17$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(authorDetailFixture) }),
  );
  await page.route(/\/api\/authors\/17\/contemporaries$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [
          { id: 101, last_name: 'Гоголь', first_name: 'Николай', born_year: 1809, died_year: 1852 },
          { id: 102, last_name: 'Пелевин', first_name: 'Виктор', born_year: 1962 },
        ],
      }),
    }),
  );
  await page.goto('/authors/17');
  const line = page.getByTestId('author-contemporaries');
  await expect(line).toHaveText('Современники: Гоголь Николай, Пелевин Виктор');
  await expect(line.getByRole('link', { name: 'Гоголь Николай' })).toHaveAttribute('href', '/authors/101');
  await expect(line.getByRole('link', { name: 'Гоголь Николай' })).toHaveAttribute('title', '1809–1852');
  await expect(line.getByRole('link', { name: 'Пелевин Виктор' })).toHaveAttribute('title', 'род. 1962');
});

test('карточка автора: без современников строки нет', async ({ mockedPage: page }) => {
  await page.route(/\/api\/authors\/17$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(authorDetailFixture) }),
  );
  await page.route(/\/api\/authors\/17\/contemporaries$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [] }) }),
  );
  await page.goto('/authors/17');
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible({ timeout: 10_000 });
  await expect(page.getByTestId('author-contemporaries')).toHaveCount(0);
});
