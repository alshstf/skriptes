import { test, expect } from './_fixtures';
import { bookDetailFixture } from './_fixtures';

/**
 * Блок «В жизни автора в это время» на карточке книги (PR-4 био-таймлайна).
 */

const lifeEvents = {
  author_id: 17,
  author_name: 'Достоевский Фёдор Михайлович',
  written_year: 1868,
  eligible: true,
  items: [
    {
      id: 4,
      source: 'wikipedia',
      type: 'loss',
      year_from: 1868,
      date_precision: 'year',
      title: 'Умерла дочь Соня',
      weight: 5,
      relation: 'same_year',
    },
    {
      id: 3,
      source: 'wikidata',
      type: 'love',
      year_from: 1867,
      date_precision: 'year',
      title: 'Брак: Анна Сниткина',
      weight: 3,
      relation: 'years_after',
      years_gap: 1,
    },
  ],
  attribution: [{ source: 'wikipedia', license: 'CC BY-SA 4.0', url: 'https://ru.wikipedia.org/wiki/X' }],
};

async function mockLife(page: Parameters<typeof mockBook>[0], body: unknown = lifeEvents) {
  await page.route(/\/api\/works\/42\/life-events$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) }),
  );
  await mockBook(page);
}

async function mockBook(page: {
  route: (u: RegExp, h: (r: { fulfill: (o: unknown) => void }) => void) => Promise<void>;
}) {
  await page.route(/\/api\/books\/1$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ...bookDetailFixture, work_id: 42 }),
    }),
  );
}

test('карточка книги: связи «в тот же год» и «за N лет до» + ссылка на таймлайн', async ({
  mockedPage: page,
}) => {
  await mockLife(page);
  await page.goto('/books/1');

  await expect(page.getByText('В жизни автора в это время')).toBeVisible();
  await expect(page.getByText('в тот же год:')).toBeVisible();
  await expect(page.getByText('Умерла дочь Соня')).toBeVisible();
  // Годовая арифметика (грабля №21): «за 1 год до», без месяцев.
  await expect(page.getByText('за 1 год до:')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Весь таймлайн →' })).toBeVisible();
});

test('карточка книги: eligible=false → блока нет', async ({ mockedPage: page }) => {
  await mockLife(page, { ...lifeEvents, eligible: false, items: [] });
  await page.goto('/books/1');
  await expect(page.getByText('В жизни автора в это время')).toHaveCount(0);
});
