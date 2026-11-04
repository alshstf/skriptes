import { test, expect } from './_fixtures';
import { test as plain, expect as plainExpect } from '@playwright/test';

// #631: ошибку скачивания браузер сохранял файлом download.json (ссылка с
// атрибутом download сохраняет любой ответ). Теперь меню сначала спрашивает
// /download/check и показывает текст сервера тостом, а ридер — на экране.

const MISSING = 'Файл книги сейчас недоступен на сервере. Попробуйте позже, а если не пройдёт — сообщите администратору.';

test('скачивание: файла нет — тост с текстом сервера, без скачивания', async ({ mockedPage: page }) => {
  let downloads = 0;
  await page.route(/\/api\/books\/19\/download\/check\?format=epub3$/, (route) =>
    route.fulfill({
      status: 404,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'archive file not available', message: MISSING }),
    }),
  );
  await page.route(/\/api\/books\/19\/download\?format=/, (route) => {
    downloads++;
    return route.fulfill({ status: 404, contentType: 'application/json', body: '{"error":"archive file not available"}' });
  });
  page.on('download', () => downloads++);

  await page.goto('/books/19');
  await page.getByRole('button', { name: 'Скачать' }).click();
  await page.getByRole('menuitem').filter({ hasText: 'EPUB 3' }).click();

  await expect(page.getByText(MISSING)).toBeVisible();
  await expect(page.getByRole('menu'), 'меню закрылось').toHaveCount(0);
  await page.waitForTimeout(300);
  expect(downloads).toBe(0);
});

test('скачивание: файл на месте — проверка, затем скачивание по той же ссылке', async ({ mockedPage: page }) => {
  let checked = false;
  await page.route(/\/api\/books\/19\/download\/check\?format=fb2$/, (route) => {
    checked = true;
    return route.fulfill({ status: 204 });
  });

  await page.goto('/books/19');
  await page.getByRole('button', { name: 'Скачать' }).click();
  const download = page.waitForEvent('download');
  await page.getByRole('menuitem').filter({ hasText: 'FB2' }).click();
  // Сам запрос скачивания page.route не перехватывает (загрузка идёт мимо
  // него) — проверяем, что после проверки браузер начал скачивание по ссылке меню.
  const d = await download;
  expect(checked).toBe(true);
  expect(new URL(d.url()).pathname + new URL(d.url()).search).toBe('/api/books/19/download?format=fb2');
  await d.cancel();
});

test('скачивание: проверка не отвечает — через 15 с тост, без скачивания', async ({ mockedPage: page }) => {
  // Диск сервера висит: ответа на проверку нет вовсе.
  await page.route(/\/api\/books\/19\/download\/check\?format=epub3$/, () => new Promise<void>(() => {}));
  let downloads = 0;
  page.on('download', () => downloads++);
  await page.clock.install();
  await page.goto('/books/19');
  await page.getByRole('button', { name: 'Скачать' }).click();
  await page.getByRole('menuitem').filter({ hasText: 'EPUB 3' }).click();
  await page.clock.runFor(16_000);
  await expect(page.getByText(/Сервер долго не отвечает/)).toBeVisible();
  expect(downloads).toBe(0);
});

plain('ридер: книга не загрузилась — текст сервера, а не «HTTP 404»', async ({ page }) => {
  await page.route('**/api/books/19/epub', (route) =>
    route.fulfill({
      status: 404,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'archive file not available', message: MISSING }),
    }),
  );
  await page.goto(`/foliate-reader.html?src=${encodeURIComponent('/api/books/19/epub')}`);
  await plainExpect(page.locator('#status')).toHaveText(`Не удалось загрузить книгу: ${MISSING}`);
});
