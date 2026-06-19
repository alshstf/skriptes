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
  await expect(setupLink).toHaveAttribute('href', '/me');
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
