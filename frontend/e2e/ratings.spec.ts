import { test, expect } from './_fixtures';
import { bookDetailFixture } from './_fixtures';

// Пользовательская оценка на карточке книги (work-level, шкала 1–5).
// Проверяем: нет сигнала «оценка читателей» → клик по звезде →
// PUT /api/works/{id}/rating → после refetch средняя появляется в строке
// сигналов (BookHeart-чип); всё в одном «оптимистичном» цикле react-query.

test('rating: ставит оценку на карточке и показывает среднюю после refetch', async ({
  mockedPage: page,
}) => {
  let userRating: number | undefined;
  let avg: number | undefined;
  let count = 0;

  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        ...bookDetailFixture,
        user_rating: userRating,
        rating_avg: avg,
        rating_count: count,
      }),
    }),
  );

  const calls: string[] = [];
  await page.route(/\/api\/works\/19\/rating$/, (route) => {
    const method = route.request().method();
    calls.push(method);
    if (method === 'PUT') {
      const body = route.request().postDataJSON() as { rating: number };
      userRating = body.rating;
      avg = body.rating;
      count = 1;
    } else {
      userRating = undefined;
      avg = undefined;
      count = 0;
    }
    void route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ rating: userRating ?? 0 }),
    });
  });

  await page.goto('/books/19');
  await expect(page.getByText('Ваша оценка:')).toBeVisible({ timeout: 10_000 });
  // Пока оценок читателей нет — BookHeart-чип в строке сигналов отсутствует.
  await expect(page.getByTitle('Оценка читателей')).toHaveCount(0);

  await page.getByRole('button', { name: 'Оценить на 5' }).click();

  await expect.poll(() => calls.length).toBeGreaterThanOrEqual(1);
  expect(calls[0]).toBe('PUT');
  // После refetch карточки средняя появляется в строке сигналов (5.0 · 1 оценка).
  await expect(page.getByTitle('Оценка читателей')).toContainText('5.0');
});
