import { test, expect } from './_fixtures';
import { authorDetailFixture } from './_fixtures';

/**
 * Био-таймлайн на карточке автора. Layout-чувствительное — только Playwright
 * (jsdom не считает CSS: grid-колонки, реальные позиции, sm-брейкпоинт).
 */

const eventsFixture = {
  enrichment_status: 'done',
  eligible: true,
  items: [
    { id: 1, source: 'wikidata', type: 'birth', year_from: 1821, date_precision: 'year', title: 'Родился', place: 'Москва', weight: 0 },
    { id: 2, source: 'wikipedia', type: 'persecution', year_from: 1849, year_to: 1854, date_precision: 'year', title: 'Арестован по делу петрашевцев', quote: 'В 1849 году арестован.', weight: 5 },
    { id: 3, source: 'wikidata', type: 'love', year_from: 1867, date_precision: 'year', title: 'Брак: Анна Сниткина', weight: 3 },
    { id: 4, source: 'wikipedia', type: 'loss', year_from: 1868, date_precision: 'year', title: 'Умерла дочь Соня', weight: 5 },
    { id: 5, source: 'wikidata', type: 'death', year_from: 1881, date_precision: 'year', title: 'Умер', weight: 0 },
  ],
  attribution: [
    { source: 'wikidata', license: 'CC0', url: 'https://www.wikidata.org/wiki/Q991' },
    { source: 'wikipedia', license: 'CC BY-SA 4.0', url: 'https://ru.wikipedia.org/wiki/X' },
  ],
};

const authorWithYears = {
  ...authorDetailFixture,
  year_stats: [
    { year: 1866, count: 1, books: [{ id: 500, title: 'Преступление и наказание' }] },
    { year: 1868, count: 1, books: [{ id: 501, title: 'Идиот' }] },
  ],
};

async function mockTimeline(page: Parameters<typeof mockAuthor>[0], events: unknown = eventsFixture) {
  await page.route(/\/api\/authors\/17\/events$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(events) }),
  );
  await mockAuthor(page);
}

async function mockAuthor(page: {
  route: (u: RegExp, h: (r: { fulfill: (o: unknown) => void }) => void) => Promise<void>;
}) {
  await page.route(/\/api\/authors\/17$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(authorWithYears),
    }),
  );
}

test('таймлайн: события слева, ось лет по центру, книги справа (десктоп)', async ({
  mockedPage: page,
}) => {
  await mockTimeline(page);
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto('/authors/17');

  // CardTitle — div, не heading: ищем по тексту заголовка секции.
  await expect(page.getByText('Жизнь и книги', { exact: true })).toBeVisible();

  // Год-ось и книга того же года: книга ПРАВЕЕ оси, событие ЛЕВЕЕ.
  const axis1868 = page.locator('li').filter({ hasText: /^1868$/ }).first();
  const book1868 = page.getByRole('link', { name: 'Идиот' });
  const event1868 = page.getByText('Умерла дочь Соня');

  const axisBox = (await axis1868.boundingBox())!;
  const bookBox = (await book1868.boundingBox())!;
  const eventBox = (await event1868.boundingBox())!;

  expect(bookBox.x).toBeGreaterThan(axisBox.x + axisBox.width - 1);
  expect(eventBox.x + eventBox.width).toBeLessThanOrEqual(axisBox.x + 1);
  // Три ячейки одного года — на одной горизонтали.
  expect(Math.abs(bookBox.y - eventBox.y)).toBeLessThan(30);
});

test('таймлайн: наведение на книгу подсвечивает годы окна [Y-2..Y]', async ({
  mockedPage: page,
}) => {
  await mockTimeline(page);
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto('/authors/17');

  await page.getByRole('link', { name: 'Идиот' }).hover();
  // Книга 1868 → подсвечены 1866, 1867, 1868 (события брака-1867 и утраты-1868).
  await expect(page.locator('[data-year="1868"][data-highlighted]').first()).toBeVisible();
  await expect(page.locator('[data-year="1867"][data-highlighted]').first()).toBeVisible();
  // 1849 — вне окна, не подсвечен.
  await expect(page.locator('[data-year="1849"][data-highlighted]')).toHaveCount(0);
});

test('таймлайн: атрибуция источников с лицензиями (CC BY-SA обязательна)', async ({
  mockedPage: page,
}) => {
  await mockTimeline(page);
  await page.goto('/authors/17');
  const footer = page.getByText(/Источники:/);
  await expect(footer).toBeVisible();
  await expect(footer).toContainText('CC BY-SA 4.0');
  await expect(page.getByRole('link', { name: 'Википедия' })).toBeVisible();
});

test('таймлайн: eligible=false → секции нет вовсе (даже скелетона)', async ({
  mockedPage: page,
}) => {
  await mockTimeline(page, { ...eventsFixture, eligible: false });
  await page.goto('/authors/17');
  // Соседняя секция на месте — значит карточка отрисовалась, а таймлайна нет.
  await expect(page.getByText('Статистика', { exact: true })).toBeVisible();
  await expect(page.getByText('Жизнь и книги', { exact: true })).toHaveCount(0);
});

test('таймлайн: на мобиле одна колонка — события и книги справа от оси', async ({
  mockedPage: page,
}) => {
  await mockTimeline(page);
  await page.setViewportSize({ width: 375, height: 812 });
  await page.goto('/authors/17');

  const axis = page.locator('li').filter({ hasText: /^1868$/ }).first();
  const book = page.getByRole('link', { name: 'Идиот' });
  const axisBox = (await axis.boundingBox())!;
  const bookBox = (await book.boundingBox())!;
  // Ось слева, контент справа; событие видно (в мобильной ячейке, не скрыто).
  expect(bookBox.x).toBeGreaterThan(axisBox.x);
  await expect(page.getByText('Умерла дочь Соня')).toBeVisible();
  // Горизонтального переполнения нет.
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(1);
});
