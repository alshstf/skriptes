import { test, expect } from './_fixtures';
import type { Page } from '@playwright/test';
import { BOOK_ID, chapterDoc, location, openReader, stubReader, waitReaderAt } from './_reader';

// Место чтения не теряется (#591): ни когда браузер возвращает фокус странице
// книги, ни когда вкладку выгрузили до сохранения, ни при повторном открытии
// ридера в той же вкладке.

test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });

const W = 390;
const H = 844;

async function swipe(page: Page, fromX: number, toX: number) {
  const cdp = await page.context().newCDPSession(page);
  const y = H / 2;
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: fromX, y }] });
  const steps = 8;
  for (let i = 1; i <= steps; i++) {
    const x = fromX + ((toX - fromX) * i) / steps;
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x, y }] });
  }
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
  await cdp.detach();
}

/** Свайпами вперёд до страницы `n` первой главы. */
async function swipeTo(page: Page, n: number) {
  const from = (await location(page))?.page ?? 1;
  for (let p = from + 1; p <= n; p++) {
    await swipe(page, W - 40, 60);
    await expect.poll(() => location(page)).toEqual({ index: 0, page: p });
  }
  // Снап-анимация доехала — ридер сообщил новое место (relocate — в её конце).
  await page.waitForTimeout(400);
}

test('фокус, который вернул браузер, не листает назад к ссылке', async ({ mockedPage: page }) => {
  await stubReader(page);
  await openReader(page);
  const doc = await chapterDoc(page, 1);
  // Тапнули сноску на первой странице — ссылка в фокусе; свайпы его не снимают.
  await doc.evaluate(() => document.getElementById('fn1')!.focus());
  await swipeTo(page, 4);
  expect(await doc.evaluate(() => document.activeElement?.id)).toBe('fn1');

  // Вернулись в окно (свернули браузер, сменили приложение): браузер снова
  // отдаёт фокус ссылке — focusin без клавиатуры и без прокрутки к элементу.
  await doc.evaluate(() => {
    const a = document.getElementById('fn1')!;
    a.blur();
    a.focus({ preventScroll: true });
  });
  await page.waitForTimeout(400);
  expect(await location(page)).toEqual({ index: 0, page: 4 });

  // Навигация с клавиатуры (Tab к ссылке) по-прежнему листает к ней.
  await doc.evaluate(() => {
    const a = document.getElementById('fn1')!;
    a.blur();
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab' }));
    a.focus({ preventScroll: true });
  });
  await expect.poll(() => location(page)).toEqual({ index: 0, page: 1 });
});

test('вкладку выгрузили до сохранения — книга открывается на последнем месте', async ({ mockedPage: page }) => {
  // Сохранения не доходят до сервера (фоновую вкладку заморозили раньше).
  const saved = await stubReader(page, () => '', { failSaves: true });
  await openReader(page);
  await swipeTo(page, 4);
  await page.reload();
  await openReader(page, { index: 0, page: 4 });
  expect(saved).toEqual([]);
});

test('читали на другом устройстве — права серверная позиция', async ({ mockedPage: page }) => {
  let server = '';
  const saved = await stubReader(page, () => server);
  await openReader(page);
  await swipeTo(page, 2);
  await expect.poll(() => saved.length, { timeout: 6_000 }).toBe(1); // дебаунс 3 с
  const page2 = saved[0].pos;
  server = page2;
  await swipeTo(page, 4);
  await expect.poll(() => saved.length, { timeout: 6_000 }).toBe(2);

  // Здесь — страница 4, и сервер про неё знает; потом на другом устройстве
  // вернулись к странице 2.
  server = page2;
  await page.reload();
  await openReader(page, { index: 0, page: 2 });
});

test('повторное открытие в той же вкладке — с места, где закрыли', async ({ mockedPage: page }) => {
  let server = '';
  const saved = await stubReader(page, () => server);
  page.on('request', (req) => {
    if (req.method() === 'PUT' && req.url().endsWith(`/api/books/${BOOK_ID}/position`)) {
      server = (req.postDataJSON() as { pos: string }).pos;
    }
  });
  await openReader(page);
  await swipeTo(page, 4);

  // На карточку и снова «Читать» — без перезагрузки страницы.
  await page.touchscreen.tap(W / 2, H / 2);
  await page.getByRole('button', { name: 'Вернуться к карточке книги' }).click();
  await expect(page).toHaveURL(/\/(works\/\d+|books\/19)$/);
  await expect.poll(() => saved.length).toBeGreaterThan(0);
  await page.getByRole('link', { name: 'Открыть книгу в браузерном ридере' }).locator('visible=true').first().click();
  await expect(page).toHaveURL(new RegExp(`/books/${BOOK_ID}/read$`));
  await waitReaderAt(page, { index: 0, page: 4 });
});
