import { test, expect } from './_fixtures';

// Премии (#389): раздел в шапке, страница премии по годам, переход к году по
// якорю с плашки карточки; без горизонтальной прокрутки на мобиле.

const award = { key: 'hugo', name: 'Хьюго', group: 'Международные' };
const wins = Array.from({ length: 40 }, (_, i) => {
  const year = 2025 - i;
  return [
    {
      id: year * 10 + 1, year, nomination: 'Роман', kind: 'work', work_id: year,
      title: `Роман ${year}`, author: 'Автор',
      item: { id: year, title: `Роман ${year} с очень длинным названием, которое не помещается в строку`,
        authors: ['Автор Длиннофамильный'], lib_id: '' },
    },
    {
      id: year * 10 + 2, year, nomination: 'Короткая повесть', kind: 'work',
      title: `Повесть ${year} без книги в библиотеке и с длинным-предлинным названием`, author: 'Кто-то Неизвестный',
      source_url: `https://fantlab.ru/work${year}`,
    },
  ];
}).flat();

async function mockAwards(page: import('@playwright/test').Page) {
  await page.route(/\/api\/awards$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [{ ...award, wins: 80, in_catalog: 40, first_year: 1986, last_year: 2025 },
          { key: 'nos', name: 'НОС', group: 'Русская литература', wins: 32, in_catalog: 28, first_year: 2009, last_year: 2021 }],
      }),
    }),
  );
  await page.route(/\/api\/awards\/hugo$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ award, wins }) }),
  );
}

test('шапка: «Премии» в навигации, ряд не переполняется на планшете', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 768, height: 900 });
  await page.goto('/books');
  const nav = page.getByRole('navigation', { name: 'Основная навигация' });
  await expect(nav.getByRole('link', { name: 'Премии' })).toBeVisible();
  await expect(nav.getByRole('link', { name: 'Полки' })).toBeVisible();
  const header = page.locator('header').first();
  const overflow = await header.evaluate((el) => el.scrollWidth - el.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0);
});

test('премии на мобиле: список и страница премии без горизонтальной прокрутки', async ({ mockedPage: page }) => {
  await mockAwards(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/awards');
  await expect(page.getByRole('link', { name: /Хьюго/ })).toBeVisible({ timeout: 10_000 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);

  await page.getByRole('link', { name: /Хьюго/ }).click();
  await expect(page.getByRole('heading', { name: '2025', exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
});

test('якорь года: страница прокручивается к году', async ({ mockedPage: page }) => {
  await mockAwards(page);
  await page.goto('/awards/hugo#y1990');
  const year = page.getByRole('heading', { name: '1990', exact: true });
  await expect(year).toBeInViewport({ timeout: 10_000 });
  await expect(page.getByRole('heading', { name: '2025', exact: true })).not.toBeInViewport();
});

test('фильтр «Премии» на /books (#447): премии со счётчиками, выбор → awards в запросе и чип', async ({ mockedPage: page }) => {
  await mockAwards(page);
  const urls: string[] = [];
  await page.route(/\/api\/books\?/, async (route) => {
    urls.push(route.request().url());
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [{ id: 1, title: 'Дюна', authors: ['Герберт Фрэнк'], lib_id: '' }],
        total: 1, limit: 30, offset: 0, processing_ms: 1,
        facets: { awards: { hugo: 3, nos: 1 } },
      }),
    });
  });
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto('/books');
  const sidebar = page.getByRole('complementary', { name: 'Фильтры' });
  await expect(sidebar.getByText('Премии', { exact: true })).toBeVisible({ timeout: 10_000 });
  await expect(sidebar.getByText('Хьюго')).toBeVisible();
  await sidebar.getByRole('checkbox', { name: /Хьюго/ }).check();
  await expect(page).toHaveURL(/awards=.*hugo/);
  await expect.poll(() => urls.some((u) => u.includes('awards=hugo'))).toBe(true);
  await expect(page.getByText('Премия: Хьюго')).toBeVisible();
});
