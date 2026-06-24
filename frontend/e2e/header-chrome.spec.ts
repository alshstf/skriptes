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
