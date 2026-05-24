import { test, expect, type Page } from './_fixtures';

/**
 * Бесконечная прокрутка /books + виртуализация. Мок отдаёт страницы по
 * offset/limit из набора в total книг. Проверяем, что скролл догружает
 * следующие страницы до конца, а DOM остаётся «оконным» (рендерятся
 * только видимые строки, не все сразу). Поведение зависит от реального
 * layout/скролла → только e2e.
 */

async function mockPagedBooks(page: Page, total: number) {
  await page.route(/\/api\/books(\?|$)/, (route) => {
    const url = new URL(route.request().url());
    const offset = Number(url.searchParams.get('offset') ?? '0');
    const limit = Number(url.searchParams.get('limit') ?? '20');
    const items = [];
    for (let i = offset; i < Math.min(offset + limit, total); i++) {
      items.push({
        id: i + 1,
        title: `Книга ${i + 1}`,
        authors: ['Автор'],
        genres: [],
        lib_id: String(i + 1),
      });
    }
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ items, total, limit, offset, processing_ms: 1, facets: {} }),
    });
  });
}

test('infinite scroll + виртуализация: догрузка до конца, DOM остаётся оконным', async ({
  mockedPage: page,
}) => {
  const TOTAL = 60; // 3 страницы по 20
  await mockPagedBooks(page, TOTAL);
  await page.goto('/books');

  await expect(page.getByRole('link', { name: 'Книга 1', exact: true })).toBeVisible({
    timeout: 10_000,
  });
  await expect(page.getByText(/^60 книг/)).toBeVisible();

  // Виртуализация: дальние книги изначально НЕ в DOM (ещё и не загружены).
  await expect(page.getByRole('link', { name: 'Книга 40', exact: true })).toHaveCount(0);

  // Скроллим к низу, пока не догрузятся все страницы (маркер «Это все
  // книги» появляется когда hasNextPage=false). Каждый scrollToBottom
  // триггерит автоподгрузку следующей страницы.
  await expect
    .poll(
      async () => {
        await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
        await page.waitForTimeout(150);
        return page.getByText('Это все книги').count();
      },
      { timeout: 20_000 },
    )
    .toBeGreaterThan(0);

  // Догрузка последней страницы увеличила высоту — доскроллим к новому
  // низу, чтобы в виртуальное окно попала последняя книга.
  await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
  await expect(page.getByRole('link', { name: 'Книга 60', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Книга 1', exact: true })).toHaveCount(0);

  // DOM оконный: отрисовано заметно меньше 60 карточек.
  const rendered = await page.getByRole('link', { name: /^Книга \d+$/ }).count();
  expect(rendered).toBeLessThan(60);
});
