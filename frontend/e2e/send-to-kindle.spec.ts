import { test, expect } from './_fixtures';
import { bookDetailFixture } from './_fixtures';

test('send-to-kindle: no targets → "Настроить Kindle" link to /me', async ({
  mockedPage: page,
}) => {
  await page.route(/\/api\/me\/kindle-targets$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '{"items":[]}' }),
  );
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(bookDetailFixture),
    }),
  );
  await page.goto('/books/19');
  const setupLink = page.getByRole('link', { name: /Настроить Kindle/ });
  await expect(setupLink).toBeVisible({ timeout: 10_000 });
  // Ведёт на /me и несёт returnTo текущей книги (возврат с профиля).
  await expect(setupLink).toHaveAttribute('href', /^\/me\?.*returnTo=/);
});

test('send-to-kindle: "Настроить Kindle" → профиль → «Назад к книге» возвращает', async ({
  mockedPage: page,
}) => {
  await page.route(/\/api\/me\/kindle-targets$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '{"items":[]}' }),
  );
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(bookDetailFixture),
    }),
  );
  await page.goto('/books/19');
  await page.getByRole('link', { name: /Настроить Kindle/ }).click();
  // На профиле, returnTo в URL, есть кнопка возврата.
  await expect(page).toHaveURL(/\/me\?.*returnTo=/);
  const back = page.getByRole('button', { name: /Назад к книге/ });
  await expect(back).toBeVisible({ timeout: 10_000 });
  await back.click();
  await expect(page).toHaveURL(/\/books\/19$/);
});

test('send-to-kindle: single target → direct send button', async ({ mockedPage: page }) => {
  await page.route(/\/api\/me\/kindle-targets$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [{ id: 1, label: 'Мой Kindle', email: 'me@kindle.com', created_at: '2026-05-15' }],
      }),
    }),
  );
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(bookDetailFixture),
    }),
  );
  let sendCalls = 0;
  await page.route(/\/api\/books\/19\/send-to-kindle$/, (route) => {
    sendCalls++;
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ status: 'sent', to: 'me@kindle.com' }),
    });
  });

  await page.goto('/books/19');
  const btn = page.getByRole('button', { name: /Отправить на Kindle/ });
  await expect(btn).toBeVisible({ timeout: 10_000 });
  await btn.click();
  await expect.poll(() => sendCalls).toBe(1);
  // Sonner-toast обычно появляется в углу.
  await expect(page.getByText(/Отправлено на/)).toBeVisible({ timeout: 5_000 });
});

test('send-to-kindle: multiple targets → dropdown with both', async ({ mockedPage: page }) => {
  await page.route(/\/api\/me\/kindle-targets$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [
          { id: 1, label: 'Мой Kindle', email: 'me@kindle.com', created_at: '2026-05-15' },
          { id: 2, label: 'Жены Kindle', email: 'wife@kindle.com', created_at: '2026-05-15' },
        ],
      }),
    }),
  );
  await page.route(/\/api\/books\/19$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(bookDetailFixture),
    }),
  );

  await page.goto('/books/19');
  await page.getByRole('button', { name: /Отправить на Kindle/ }).click();
  await expect(page.getByText('Куда отправить?')).toBeVisible();
  await expect(page.getByText('Мой Kindle')).toBeVisible();
  await expect(page.getByText('Жены Kindle')).toBeVisible();
});

test('profile: адрес отправителя и подсказка «Как разрешить его в Amazon»', async ({
  mockedPage: page,
}) => {
  await page.route(/\/api\/me\/kindle-targets$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ items: [], sender: 'books@example.com' }),
    }),
  );
  await page.goto('/me');
  await expect(page.getByText('books@example.com')).toBeVisible({ timeout: 10_000 });
  await page.getByRole('button', { name: /Как разрешить его в Amazon/ }).click();
  const pop = page.getByRole('dialog');
  await expect(pop).toContainText('Approved Personal Document E-mail List');
  await expect(pop.getByRole('link', { name: 'amazon.com/mycd' })).toHaveAttribute(
    'href',
    'https://www.amazon.com/mycd',
  );
  if (process.env.KINDLE_HINT_SHOT) await page.screenshot({ path: process.env.KINDLE_HINT_SHOT });
});

test('profile: подсказка Amazon на телефоне помещается в экран', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await page.route(/\/api\/me\/kindle-targets$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ items: [], sender: 'very.long.sender.address@example.com' }),
    }),
  );
  await page.goto('/me');
  await page.getByRole('button', { name: /Как разрешить его в Amazon/ }).click();
  const box = await page.getByRole('dialog').boundingBox();
  expect(box).not.toBeNull();
  expect(box!.x).toBeGreaterThanOrEqual(0);
  expect(box!.x + box!.width).toBeLessThanOrEqual(375);
  if (process.env.KINDLE_HINT_SHOT_MOBILE) {
    await page.screenshot({ path: process.env.KINDLE_HINT_SHOT_MOBILE });
  }
});

test('profile: без почты на сервере — «отправка не настроена», без подсказки', async ({
  mockedPage: page,
}) => {
  await page.route(/\/api\/me\/kindle-targets$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ items: [], sender: '' }),
    }),
  );
  await page.goto('/me');
  await expect(page.getByText(/Отправка на Kindle на сервере пока не настроена/)).toBeVisible({
    timeout: 10_000,
  });
  await expect(page.getByRole('button', { name: /Как разрешить его в Amazon/ })).toHaveCount(0);
});
