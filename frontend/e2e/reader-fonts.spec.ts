import { test, expect } from './_fixtures';
import type { Frame, Page } from '@playwright/test';
import { chapterDoc, openReader, stubReader } from './_reader';

// Меню «Шрифт» ридера: свободные шрифты по мотивам Kindle (public/fonts). Шрифт
// подключается в документ книги через @import fonts.css — проверяем, что файл
// реально грузится (CSP, путь из blob-документа), а не только font-family.

test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });

const paragraphFont = (doc: Frame) => doc.evaluate(() => getComputedStyle(document.getElementById('p1-1')!).fontFamily);

/** Загрузился ли шрифт в документе книги (FontFace из fonts.css с кириллицей). */
const fontLoaded = (doc: Frame, family: string) =>
  doc.evaluate(async (f) => (await document.fonts.load(`16px "${f}"`, 'Книга')).length > 0, family);

async function chooseFont(page: Page, name: string) {
  await page.getByRole('button', { name: 'Настройки чтения' }).click();
  const sheet = page.getByRole('dialog', { name: 'Настройки чтения' });
  await sheet.getByRole('button', { name }).click();
  await expect(sheet.getByRole('button', { name })).toHaveAttribute('aria-pressed', 'true');
  await page.keyboard.press('Escape');
  await expect(sheet).toBeHidden();
}

test('шрифт по умолчанию — книжный Literata, файл грузится в документ книги', async ({ mockedPage: page }) => {
  await stubReader(page);
  await openReader(page);
  const doc = await chapterDoc(page, 1);
  expect(await paragraphFont(doc)).toMatch(/^Literata,/);
  await expect.poll(() => fontLoaded(doc, 'Literata')).toBe(true);
});

test('меню «Шрифт»: выбор применяется к книге сразу, «Как в книге» снимает шрифт', async ({ mockedPage: page }) => {
  await stubReader(page);
  await openReader(page);
  const doc = await chapterDoc(page, 1);

  await chooseFont(page, 'PT Serif — классический');
  await expect.poll(() => paragraphFont(doc)).toMatch(/^"PT Serif",/);
  await expect.poll(() => fontLoaded(doc, 'PT Serif')).toBe(true);
  // Заголовок главы — тем же шрифтом.
  expect(await doc.evaluate(() => getComputedStyle(document.querySelector('h1')!).fontFamily)).toMatch(/^"PT Serif",/);

  await chooseFont(page, 'Как в книге — стиль издания');
  await expect.poll(() => paragraphFont(doc)).not.toMatch(/Literata|PT Serif/);

  // Выбор живёт на устройстве.
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem('skriptes.reader')!).font)).toBe('book');
});

test('в меню название шрифта показано самим шрифтом', async ({ mockedPage: page }) => {
  await stubReader(page);
  await openReader(page);
  await page.getByRole('button', { name: 'Настройки чтения' }).click();
  const sheet = page.getByRole('dialog', { name: 'Настройки чтения' });
  await expect(sheet.getByRole('button', { name: /—/ })).toHaveCount(7);
  const sample = sheet.getByRole('button', { name: 'OpenDyslexic — для дислексии' }).locator('span').first();
  await expect(sample).toHaveCSS('font-family', /^OpenDyslexic,/);
  await expect.poll(() => page.evaluate(() => document.fonts.check('16px OpenDyslexic', 'Аа'))).toBe(true);
  await page.evaluate(() => document.fonts.ready);
  // Названия помещаются в плитку (OpenDyslexic широкий — его плитка во всю ширину).
  const truncated = await sheet.getByRole('button', { name: /—/ }).evaluateAll((buttons) =>
    buttons.filter((b) => {
      const name = b.querySelector('span')!;
      return name.scrollWidth > name.clientWidth;
    }).map((b) => b.getAttribute('aria-label')),
  );
  expect(truncated).toEqual([]);
});
