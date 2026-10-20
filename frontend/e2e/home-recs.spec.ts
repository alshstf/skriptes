import { test, expect } from './_fixtures';

// Лента Главной (план recsys/01, 1.3): у карточек есть причина «почему это
// здесь», причина не вылезает за ширину карточки, скрытие — с причиной через
// kebab, и карточка исчезает из секции.

type Item = {
  id: number;
  work_id: number;
  title: string;
  authors: string[];
  lib_id: string;
  series?: string;
  reasons: Record<string, unknown>[];
};

const CONTINUE: Item = {
  id: 11,
  work_id: 1011,
  title: 'Недочитанная книга',
  authors: ['Автор Первый'],
  lib_id: 'L11',
  reasons: [{ kind: 'in_progress', fraction: 0.42, at: new Date(Date.now() - 3 * 86_400_000).toISOString() }],
};

const FEED: Item = {
  id: 12,
  work_id: 1012,
  title: 'Очень длинное название новинки подписанного автора, которое должно обрезаться',
  authors: ['Автор Подписанный'],
  lib_id: 'L12',
  series: 'Мой цикл',
  reasons: [
    { kind: 'new_by_author', author_id: 5, author: 'Автор Подписанный Имя Отчество', at: '2026-10-19' },
  ],
};

function mockHome(page: import('@playwright/test').Page, feed: { ref: Item[] }) {
  page.route(/\/api\/me\/recommendations\?/, (route) => {
    const section = new URL(route.request().url()).searchParams.get('section');
    const items = section === 'continue_reading' ? [CONTINUE] : section === 'subscriptions' ? feed.ref : [];
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ section, items }),
    });
  });
}

test('home: причины под карточками и ссылка на автора', async ({ mockedPage: page }) => {
  mockHome(page, { ref: [FEED] });
  await page.goto('/');

  await expect(page.getByRole('heading', { name: 'Продолжить чтение' })).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText('Остановились на 42 % · 3 дня назад')).toBeVisible();

  const feedReason = page.getByTestId('rec-reason').filter({ hasText: 'По подписке на автора' });
  await expect(feedReason).toBeVisible();
  const link = feedReason.getByRole('link', { name: 'Автор Подписанный Имя Отчество' });
  await expect(link).toHaveAttribute('href', '/authors/5');

  // Причина не шире карточки (line-clamp, без горизонтального переполнения).
  const card = page.getByRole('link', { name: /Очень длинное название новинки/ });
  const cardBox = await card.boundingBox();
  const reasonBox = await feedReason.boundingBox();
  expect(cardBox && reasonBox).toBeTruthy();
  expect(reasonBox!.width).toBeLessThanOrEqual(cardBox!.width + 1);
  expect(reasonBox!.height).toBeLessThanOrEqual(3 * 16 + 2); // не больше двух строк xs

  // Клик по ссылке причины ведёт на автора, не на книгу.
  await link.click();
  await expect(page).toHaveURL(/\/authors\/5$/);
});

test('home: «Уже читал» скрывает новинку с причиной', async ({ mockedPage: page }) => {
  const feed = { ref: [FEED] };
  mockHome(page, feed);
  let body: unknown;
  await page.route(/\/api\/me\/feed\/dismiss$/, (route) => {
    body = route.request().postDataJSON();
    feed.ref = [];
    void route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ dismissed: true }) });
  });

  await page.goto('/');
  await expect(page.getByText(/Очень длинное название новинки/)).toBeVisible({ timeout: 10_000 });

  await page.getByRole('button', { name: 'Скрыть из ленты' }).click();
  await page.getByRole('menuitem', { name: 'Уже читал' }).click();

  await expect(page.getByText(/Очень длинное название новинки/)).toHaveCount(0);
  expect(body).toEqual({ work_id: 1012, reason: 'already_read' });
});
