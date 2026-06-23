import { test, expect } from './_fixtures';
import { bookDetailFixture } from './_fixtures';

// Редизайн карточки книги (1.3.x): компактная строка сигналов, технические поля
// под раскрывашкой «Детали файла», сворачивание длинной аннотации. jsdom не
// считает CSS-layout (line-clamp, видимость внутри закрытого <details>) —
// поэтому эти проверки только в Playwright (граблю №4).

test('детали файла: свёрнуты по умолчанию, раскрываются по клику', async ({
  mockedPage: page,
}) => {
  await page.goto('/books/19');
  const summary = page.getByText('Детали файла');
  await expect(summary).toBeVisible({ timeout: 10_000 });

  // Размер (formatBytes(849047) → «829.1 КБ») скрыт пока <details> закрыт.
  await expect(page.getByText('829.1 КБ')).toBeHidden();
  await summary.click();
  await expect(page.getByText('829.1 КБ')).toBeVisible();

  // Размер в человекочитаемых единицах, не сырых KiB (регрессия редизайна).
  await expect(page.getByText(/KiB/)).toHaveCount(0);
});

test('строка сигналов: внешний рейтинг + источник в тултипе', async ({ mockedPage: page }) => {
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      // rating (LIBRATE) → externalRatingDisplay → «4.2 · библиотека».
      body: JSON.stringify({ ...bookDetailFixture, rating: 4.2 }),
    }),
  );

  await page.goto('/books/19');
  const rating = page.getByText('4.2', { exact: true });
  await expect(rating).toBeVisible({ timeout: 10_000 });

  // Источник — в тултипе по ховеру (Globe-чип), не текстом рядом.
  await rating.hover();
  await expect(page.getByText('Внешний рейтинг · библиотека')).toBeVisible({ timeout: 5_000 });
});

test('аннотация: длинная сворачивается, «Развернуть» раскрывает', async ({
  mockedPage: page,
}) => {
  const longAnnotation = Array.from(
    { length: 30 },
    (_, i) =>
      `Параграф номер ${i + 1} с достаточно длинным предложением, чтобы текст ` +
      `гарантированно переносился на несколько строк в карточке книги.`,
  ).join(' ');

  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ...bookDetailFixture, annotation: longAnnotation }),
    }),
  );

  await page.goto('/books/19');
  // Длинный текст обрезан по строкам → видна кнопка «Развернуть».
  const expand = page.getByRole('button', { name: 'Развернуть' });
  await expect(expand).toBeVisible({ timeout: 10_000 });
  await expand.click();
  await expect(page.getByRole('button', { name: 'Свернуть' })).toBeVisible();
});
