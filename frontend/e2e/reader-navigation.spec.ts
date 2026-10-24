import { test, expect } from './_fixtures';
import type { Page } from '@playwright/test';
import { location, openReader, stubReader } from './_reader';

// Навигация по книге (#592, #593): оглавление отдельной кнопкой, слайдер
// прогресса — индикатор и быстрый переход, «Вернуться» после прыжка.

test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });

const W = 390;
const H = 844;

const header = (page: Page) => page.locator('header').filter({ has: page.getByRole('button', { name: 'Настройки чтения' }) });
const slider = (page: Page) => page.getByRole('slider', { name: 'Прогресс книги' });

async function toPage3(page: Page) {
  await page.touchscreen.tap(W - 30, H / 2);
  await page.touchscreen.tap(W - 30, H / 2);
  await expect.poll(() => location(page)).toEqual({ index: 0, page: 3 });
  await page.waitForTimeout(400); // relocate — в конце анимации
  await page.touchscreen.tap(W / 2, H / 2); // панель
  await expect(header(page)).not.toHaveAttribute('inert');
}

test('оглавление: главы, текущая отмечена, переход и «Вернуться»', async ({ mockedPage: page }) => {
  await stubReader(page);
  await openReader(page);
  await toPage3(page);

  await page.getByRole('button', { name: 'Оглавление' }).click();
  const sheet = page.getByRole('dialog', { name: 'Оглавление' });
  await expect(sheet.getByRole('listitem')).toHaveText(['Глава 1', 'Глава 2']);
  await expect(sheet.getByRole('button', { name: 'Глава 1' })).toHaveAttribute('aria-current', 'location');

  await sheet.getByRole('button', { name: 'Глава 2' }).click();
  await expect(sheet).toBeHidden();
  await expect.poll(() => location(page)).toEqual({ index: 1, page: 1 });
  await expect(header(page)).toContainText('Глава 2');

  // Передумали — обратно на страницу 3 первой главы.
  await page.getByRole('button', { name: /Вернуться на прежнее место/ }).click();
  await expect.poll(() => location(page)).toEqual({ index: 0, page: 3 });
  await expect(page.getByRole('button', { name: /Вернуться на прежнее место/ })).toHaveCount(0);
});

test('слайдер прогресса: показывает долю книги, переносит по отпусканию, «Вернуться»', async ({ mockedPage: page }) => {
  await stubReader(page);
  await openReader(page);
  await toPage3(page);

  // Слайдер — у нижнего края, ниже страницы книги не уходит.
  await expect
    .poll(async () => {
      const box = await slider(page).boundingBox();
      return box ? box.y + box.height : Infinity;
    })
    .toBeLessThanOrEqual(H);
  const start = Number(await slider(page).inputValue());
  expect(start).toBeGreaterThan(0);
  expect(start).toBeLessThan(500);

  await slider(page).fill('750');
  await expect.poll(() => location(page).then((l) => l?.index)).toBe(1);
  await expect.poll(async () => Number(await slider(page).inputValue())).toBeGreaterThan(600);

  // Повторный прыжок не перетирает место, куда вернуться.
  await slider(page).fill('900');
  await page.getByRole('button', { name: /Вернуться на прежнее место/ }).click();
  await expect.poll(() => location(page)).toEqual({ index: 0, page: 3 });
  await expect.poll(async () => Number(await slider(page).inputValue())).toBe(start);
});

test('панель спрятана — слайдер тоже (не перехватывает тапы)', async ({ mockedPage: page }) => {
  await stubReader(page);
  await openReader(page);
  const bar = page.locator('div').filter({ has: page.locator('input[type=range]') }).last();
  await expect(slider(page)).toBeVisible();
  await expect(bar).not.toHaveAttribute('inert');
  await page.touchscreen.tap(W - 30, H / 2);
  await expect(header(page)).toHaveAttribute('inert', '');
  await expect(page.locator('div[inert]').filter({ has: page.locator('input[type=range]') })).toHaveCount(1);
  // Тап у нижнего края листает, а не двигает слайдер.
  await page.touchscreen.tap(W - 30, H - 30);
  await expect.poll(() => location(page)).toEqual({ index: 0, page: 3 });
});
