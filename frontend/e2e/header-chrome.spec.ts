import { test, expect } from './_fixtures';

// Регресс iOS-сепарации/safe-area (грабля №18). env(safe-area-*) в Chromium = 0,
// поэтому проверяем НЕ визуальный инсет, а что классы/тень на месте — чтобы фикс
// не «отвалился» при будущих правках хэдера/дровера.

test('header: имеет тень-сепарацию (не сливается с контентом при скролле)', async ({
  mockedPage: page,
}) => {
  await page.goto('/books');
  const header = page.locator('header').first();
  await expect(header).toBeVisible();
  const shadow = await header.evaluate((el) => getComputedStyle(el).boxShadow);
  // Tailwind композирует box-shadow с ring-переменными; ключевой признак нашей
  // тени — её радиус размытия 22px. Без тени было бы 'none'.
  expect(shadow).not.toBe('none');
  expect(shadow).toContain('22px');
});

test('mobile nav drawer: несёт safe-area паддинг (pt-safe) — не лезет под статус-бар', async ({
  mockedPage: page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 }); // мобила → виден бургер
  await page.goto('/books');
  await page.getByRole('button', { name: 'Открыть меню' }).click();
  const sheet = page.locator('[data-slot=sheet-content]');
  await expect(sheet).toBeVisible();
  await expect(sheet).toHaveClass(/pt-safe/);
  await expect(sheet).toHaveClass(/pb-safe/);
});

test('command palette: top с safe-area-инсетом (не под статус-баром на мобиле)', async ({
  mockedPage: page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/books');
  await page.getByRole('button', { name: 'Открыть поиск' }).click();
  const dialog = page.locator('[data-slot=dialog-content]');
  await expect(dialog).toBeVisible();
  // На мобиле палитра прижата к верху → top должен учитывать safe-area-инсет
  // (а не голый top-4, который уезжал под статус-бар).
  const cls = (await dialog.getAttribute('class')) ?? '';
  expect(cls).toContain('env(safe-area-inset-top)');
});

// #578: на iOS 26+ PWA под статус-баром — полоса системного размытия ~20pt;
// шапка опускает кнопки ниже неё (pt-safe-ui), а всё, что липнет под шапку, и
// прокрутка к якорям считают отступ от --header-h. Инсеты iPhone — через CDP,
// как в reader-mobile.spec.ts.
test('safe-area: кнопки шапки ниже полосы размытия, саб-бар /books липнет ровно под шапку', async ({
  mockedPage: page,
}) => {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('Emulation.setSafeAreaInsetsOverride' as never, { insets: { top: 59, bottom: 34 } } as never);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/books');
  const BAND = 59 + 20;
  const header = page.locator('header').first();
  await expect(header).toHaveClass(/pt-safe-ui/);
  // Кнопки шапки — ниже полосы, не у самого инсета (было inset+10).
  const menu = (await page.getByRole('button', { name: 'Открыть меню' }).boundingBox())!;
  expect(menu.y).toBeGreaterThanOrEqual(BAND);
  const hb = (await header.boundingBox())!;
  const headerBottom = hb.y + hb.height;

  // Саб-бар фильтров на мобиле — sticky с top = --header-h: сразу под шапкой,
  // а не под ней (раньше считался от голого env()).
  const bar = page.locator('div.sticky').filter({ has: page.getByRole('button', { name: /Фильтры/ }) }).first();
  await expect(bar).toBeVisible();
  const top = await bar.evaluate((el) => parseFloat(getComputedStyle(el).top));
  expect(Math.abs(top - headerBottom)).toBeLessThanOrEqual(1.5);
  // Прокрутка к якорю учитывает шапку.
  const pad = await page.evaluate(() => parseFloat(getComputedStyle(document.documentElement).scrollPaddingTop));
  expect(pad).toBeGreaterThanOrEqual(headerBottom);
});
