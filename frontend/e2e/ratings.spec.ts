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

// «Раз понравилось — вот следующая» (план recsys/02, 2.8): после оценки ≥ 4 на
// карточке — до трёх карточек правил с причинами; оценка ≤ 3 блок не показывает.
test('rating: после оценки 5 — карточки «вот следующая», после 2 — нет', async ({ mockedPage: page }) => {
  let userRating: number | undefined;
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ...bookDetailFixture, user_rating: userRating }),
    }),
  );
  await page.route(/\/api\/works\/19\/rating$/, (route) => {
    if (route.request().method() === 'PUT') {
      userRating = (route.request().postDataJSON() as { rating: number }).rating;
    }
    void route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ rating: userRating ?? 0 }) });
  });
  const related: string[] = [];
  await page.route(/\/api\/me\/recommendations\/related\?/, (route) => {
    related.push(new URL(route.request().url()).search);
    void route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        work_id: 19,
        items: [
          {
            id: 101,
            work_id: 1101,
            title: 'Кадетский корпус. Книга 3',
            authors: ['Алексеев Евгений Артёмович'],
            lib_id: 'L101',
            reasons: [
              { kind: 'next_in_series', series_id: 7, series: 'Петля', work_id: 19, title: 'Кадетский корпус. Книга 2', ser_no: 3 },
            ],
          },
        ],
      }),
    });
  });

  await page.goto('/books/19');
  await expect(page.getByText('Ваша оценка:')).toBeVisible({ timeout: 10_000 });
  await expect(page.getByTestId('after-rating-recs')).toHaveCount(0);

  await page.getByRole('button', { name: 'Оценить на 5' }).click();
  await expect(page.getByTestId('after-rating-recs')).toBeVisible();
  await expect(page.getByText('Раз понравилось — вот следующая')).toBeVisible();
  await expect(page.getByRole('link', { name: /Кадетский корпус\. Книга 3/ })).toHaveAttribute('href', '/works/1101');
  expect(related[0]).toContain('work_id=19');

  await page.getByRole('button', { name: 'Оценить на 2' }).click();
  await expect(page.getByTestId('after-rating-recs')).toHaveCount(0);
});
