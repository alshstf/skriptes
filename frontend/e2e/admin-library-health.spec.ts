import type { Page } from '@playwright/test';
import { test, expect } from './_fixtures';

// #632: плашка под вкладками админки, когда бэкенд не видит архивы с книгами.

async function mockLibrary(page: Page, missing: number, sample: string[] = [], readable = true, read_error = '') {
  await page.route(/\/api\/admin\/library$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        checked: true,
        checked_at: '2026-11-03T10:00:00Z',
        archives: 229,
        missing,
        sample,
        readable,
        read_error,
      }),
    }),
  );
}

test('админка: не видно ни одного архива — плашка про том с книгами', async ({ mockedPage: page }) => {
  await mockLibrary(page, 229, ['fb2-000024-030559.zip']);
  await page.goto('/admin/hidden');
  await expect(page.getByText('Сервер не видит ни одного архива с книгами')).toBeVisible();
  await expect(page.getByText(/не подключён том с книгами/)).toBeVisible();
});

test('админка: часть архивов пропала — сколько и какие', async ({ mockedPage: page }) => {
  await mockLibrary(page, 2, ['fb2-1.zip', 'fb2-2.zip']);
  await page.goto('/admin/hidden');
  await expect(page.getByText('Сервер не видит 2 из 229 архивов с книгами')).toBeVisible();
  await expect(page.getByText('Например: fb2-1.zip, fb2-2.zip')).toBeVisible();
});

test('админка: всё на месте — плашки нет', async ({ mockedPage: page }) => {
  await mockLibrary(page, 0);
  await page.goto('/admin/hidden');
  await expect(page.getByRole('navigation', { name: 'Администрирование' })).toBeVisible();
  await expect(page.getByText(/Сервер не видит/)).toHaveCount(0);
});

test('админка: архивы на месте, но не читаются — плашка про хранилище', async ({ mockedPage: page }) => {
  await mockLibrary(page, 0, [], false, 'books volume does not respond');
  await page.goto('/admin/hidden');
  await expect(page.getByText('Хранилище с книгами не отвечает')).toBeVisible();
  await expect(page.getByText('Ошибка: books volume does not respond')).toBeVisible();
  await expect(page.getByText(/Сервер не видит/)).toHaveCount(0);
});
