import { test, expect } from './_fixtures';

// Блок «Оцените прочитанное» на Главной: оценил/скрыл → книга уходит из ленты.
// Мокаем фид запросов оценки + пустые остальные динам-блоки Главной.

const ITEM = {
  id: 21,
  work_id: 21,
  title: 'Тестовая книга для оценки',
  authors: ['Автор Тестовый'],
  lib_id: 'L21',
};

function mockHomeFeeds(page: import('@playwright/test').Page, feed: { ref: typeof ITEM[] }) {
  page.route(/\/api\/me\/rating-prompts\/feed/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ items: feed.ref }),
    }),
  );
  page.route(/\/api\/me\/continue-reading/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [] }) }),
  );
  page.route(/\/api\/me\/feed\/subscriptions/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [] }) }),
  );
}

test('home: оценил книгу в «Оцените прочитанное» → ушла из блока', async ({ mockedPage: page }) => {
  const feed = { ref: [ITEM] };
  mockHomeFeeds(page, feed);
  await page.route(/\/api\/works\/21\/rating$/, (route) => {
    feed.ref = [];
    void route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ rating: 4 }) });
  });

  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Оцените прочитанное' })).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText('Тестовая книга для оценки')).toBeVisible();

  await page.getByRole('button', { name: 'Оценить на 4' }).click();

  await expect(page.getByText('Тестовая книга для оценки')).toHaveCount(0);
});

test('home: «Не буду оценивать» убирает книгу из блока', async ({ mockedPage: page }) => {
  const feed = { ref: [ITEM] };
  mockHomeFeeds(page, feed);
  await page.route(/\/api\/works\/21\/rating-prompt\/dismiss$/, (route) => {
    feed.ref = [];
    void route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ dismissed: true }) });
  });

  await page.goto('/');
  await expect(page.getByText('Тестовая книга для оценки')).toBeVisible({ timeout: 10_000 });

  await page.getByRole('button', { name: 'Действия с запросом оценки' }).click();
  await page.getByRole('menuitem', { name: 'Не буду оценивать' }).click();

  await expect(page.getByText('Тестовая книга для оценки')).toHaveCount(0);
});
