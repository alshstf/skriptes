import { test, expect } from './_fixtures';
import type { Page } from '@playwright/test';
import { chapterDoc, location, openReader, stubReader } from './_reader';

// Страницы листались «сами» (#607): снап после касания листал по скорости
// последнего touchmove, а iOS шлёт его на любой сдвиг — палец дёрнулся на 5px
// за 8мс при отрыве, и страница уезжала. Chromium гасит сдвиги до ~15px
// (touch slop), поэтому касания здесь — синтетические TouchEvent в документе
// главы, с шагом 8мс, как у iPhone (120 Гц).

test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });

/** Касание: палец лёг, полежал, сдвинулся по шагам [dx, dy] с шагом 8мс, оторвался. */
async function touch(page: Page, steps: [number, number][]) {
  const doc = await chapterDoc(page, 1);
  await doc.evaluate(async (steps) => {
    const target = document.body;
    let x = 200;
    let y = 400;
    const ev = (type: string, active: boolean) => {
      const t = new Touch({ identifier: 1, target, clientX: x, clientY: y, screenX: x, screenY: y, pageX: x, pageY: y });
      return new TouchEvent(type, { bubbles: true, cancelable: true, touches: active ? [t] : [], targetTouches: active ? [t] : [], changedTouches: [t] });
    };
    const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
    target.dispatchEvent(ev('touchstart', true));
    await sleep(400);
    for (const [dx, dy] of steps) {
      x -= dx;
      y -= dy;
      target.dispatchEvent(ev('touchmove', true));
      await sleep(8);
    }
    target.dispatchEvent(ev('touchend', false));
  }, steps);
  await page.waitForTimeout(600);
}

test('палец дёрнулся при отрыве — страница не листается', async ({ mockedPage: page }) => {
  await stubReader(page);
  await openReader(page);
  await touch(page, [[0, 0], [0, 0], [5, 0]]);
  expect(await location(page)).toEqual({ index: 0, page: 1 });
  await touch(page, [[3, 1], [6, 2], [8, 1]]); // 17px быстро — всё ещё не свайп
  expect(await location(page)).toEqual({ index: 0, page: 1 });
});

test('вертикальное движение с боковым дрожанием не листает', async ({ mockedPage: page }) => {
  await stubReader(page);
  await openReader(page);
  await touch(page, [[2, 15], [4, 20], [6, 25], [9, 20]]);
  expect(await location(page)).toEqual({ index: 0, page: 1 });
});

test('настоящий короткий свайп листает', async ({ mockedPage: page }) => {
  await stubReader(page);
  await openReader(page);
  await touch(page, [[10, 0], [15, 1], [20, 0]]); // 45px быстро влево
  await expect.poll(() => location(page)).toEqual({ index: 0, page: 2 });
});
