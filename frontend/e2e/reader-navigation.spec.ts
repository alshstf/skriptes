import { test, expect } from './_fixtures';
import type { Page } from '@playwright/test';
import { chapterDoc, location, openReader, readerFrame, stubReader } from './_reader';

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

test('слайдер прогресса: показывает долю книги, переносит, «Вернуться»', async ({ mockedPage: page }) => {
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

test('слайдер: страница едет за ползунком до отпускания (#600)', async ({ mockedPage: page }) => {
  await stubReader(page);
  await openReader(page);
  const box = (await slider(page).boundingBox())!;
  const y = box.y + box.height / 2;
  await page.mouse.move(box.x + 4, y);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width * 0.8, y, { steps: 12 });
  // Ещё не отпустили — а вторая глава уже на экране.
  await expect.poll(() => location(page).then((l) => l?.index)).toBe(1);
  // Появилась «Вернуться» — а слайдер под пальцем не сдвинулся (строкой выше).
  await expect(page.getByRole('button', { name: /Вернуться на прежнее место/ })).toBeVisible();
  const after = (await slider(page).boundingBox())!;
  expect(Math.round(after.x)).toBe(Math.round(box.x));
  expect(Math.round(after.y)).toBe(Math.round(box.y));
  expect(Math.round(after.width)).toBe(Math.round(box.width));
  await page.mouse.move(box.x + box.width * 0.3, y, { steps: 8 });
  await expect.poll(() => location(page).then((l) => l?.index)).toBe(0);
  await page.mouse.up();
  await expect(page.getByRole('button', { name: /Вернуться на прежнее место/ })).toBeVisible();
});

test('страница без колонтитулов; глава — в панели, процента там нет (#600)', async ({ mockedPage: page }) => {
  await stubReader(page);
  await openReader(page);
  await page.touchscreen.tap(W - 30, H / 2);
  await expect.poll(() => location(page)).toEqual({ index: 0, page: 2 });
  const marginals = await readerFrame(page)!.evaluate(() => {
    const r = (document.querySelector('foliate-view') as unknown as { renderer: { heads?: Element[]; feet?: Element[] } }).renderer;
    return [...(r.heads ?? []), ...(r.feet ?? [])].map((el) => el.textContent ?? '').join('');
  });
  expect(marginals).toBe('');
  await page.touchscreen.tap(W / 2, H / 2);
  await expect(header(page)).toContainText('Глава 1');
  await expect(header(page)).not.toContainText('%');
});

test('прыжок слайдером к концу книгу прочитанной не делает (#600)', async ({ mockedPage: page }) => {
  // «Прочитано» — только перелистыванием до конца (доля ≥ 0.99; у короткой
  // тестовой книги последняя страница — ≈ 0.95, поэтому здесь — только запрет).
  await stubReader(page);
  let marked = 0;
  await page.route(/\/api\/books\/19\/read$/, (route) => {
    if (route.request().method() === 'POST') marked++;
    return route.fulfill({ status: 200, contentType: 'application/json', body: '{"is_read":true}' });
  });
  await openReader(page);
  await slider(page).fill('1000');
  await expect.poll(() => location(page).then((l) => l?.index)).toBe(1);
  await page.waitForTimeout(800);
  expect(marked).toBe(0);
  await expect(page.getByText('Книга отмечена как прочитанная')).toHaveCount(0);
});

// fb2cng ставит заголовку главы разрыв страницы; в начале секции WebKit рисует
// его пустой колонкой — пустая страница перед главой (#610). Chromium разрыв в
// начале и так пропускает, поэтому проверяем сам стиль: снят.
test('разрыв страницы перед первым элементом главы снят (#610)', async ({ mockedPage: page }) => {
  await stubReader(page);
  await openReader(page);
  const doc = await chapterDoc(page, 1);
  const breaks = await doc.evaluate(() =>
    ['.main-body', '.section-title', 'h1'].map((sel) => getComputedStyle(document.querySelector(sel)!).breakBefore),
  );
  expect(breaks).toEqual(['auto', 'auto', 'auto']);
  // Разрывы дальше в тексте не трогаем — только цепочку первых элементов.
  const inner = await doc.evaluate(() => {
    const p = document.getElementById('p1-2')!;
    p.style.breakBefore = 'page';
    return getComputedStyle(p).breakBefore;
  });
  expect(inner).toBe('page');
});
