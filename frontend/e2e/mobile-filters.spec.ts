import { test, expect } from './_fixtures';

/**
 * Мобильный редизайн /books: на узком вьюпорте фильтры прячутся в
 * боковой drawer по кнопке рядом с поиском (десктопный sidebar скрыт).
 * Эти проверки чувствительны к CSS-брейкпоинтам (md:hidden /
 * hidden md:block), которые jsdom не вычисляет — поэтому только e2e.
 */

test.describe('mobile /books (375px)', () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test('фильтры скрыты в drawer, открываются кнопкой рядом с поиском', async ({
    mockedPage: page,
  }) => {
    await page.goto('/books');
    await expect(page.getByPlaceholder('Поиск по названию или автору')).toBeVisible({
      timeout: 10_000,
    });

    // Десктопный sidebar-заголовок «Фильтры» (heading) НЕ виден на мобильном
    // (родитель hidden md:block → display:none).
    await expect(page.getByRole('heading', { name: 'Фильтры' })).toBeHidden();

    // Кнопка фильтров (icon, aria-label «Фильтры») видна.
    const filterBtn = page.getByRole('button', { name: 'Фильтры', exact: true });
    await expect(filterBtn).toBeVisible();

    // Первая книга списка видна без скролла мимо длинного фильтр-блока.
    await expect(page.getByText('Кадетский корпус. Книга 2')).toBeVisible();

    // Открываем drawer → внутри фильтры с категориями жанров.
    await filterBtn.click();
    const sheet = page.locator('[data-slot="sheet-content"]');
    await expect(sheet).toBeVisible();
    await expect(sheet.getByText('Фантастика')).toBeVisible();

    // Раскрываем «Фантастика», отмечаем leaf-жанр.
    const fantasyRow = sheet.locator('div').filter({ hasText: /^Фантастика/ }).first();
    await fantasyRow.getByRole('button', { name: 'Развернуть' }).click();
    const leaf = sheet.getByRole('checkbox', { name: /Боевая фантастика/ });
    await expect(leaf).toBeVisible();
    await leaf.check();

    // Фильтр применился сразу (URL), даже не закрывая drawer.
    await expect(page).toHaveURL(/sf_action/);

    // Закрываем по кнопке футера «Показать …».
    await sheet.getByRole('button', { name: /Показать/ }).click();
    await expect(sheet).toBeHidden();

    // Чипсы выбранных фильтров на мобильном скрыты (hidden md:block) —
    // их роль берёт бейдж-счётчик на кнопке. Сам чип в DOM есть (для
    // десктопа), но не виден.
    await expect(page.getByText(/Жанр: Боевая фантастика/)).toBeHidden();
    // Бейдж счётчика на кнопке фильтра отражает выбор.
    await expect(filterBtn.getByText('1')).toBeVisible();
  });

  test('кнопка быстрого сброса очищает фильтры', async ({ mockedPage: page }) => {
    await page.goto('/books?genres=sf_action');
    const reset = page.getByRole('button', { name: 'Сбросить фильтры' });
    await expect(reset).toBeVisible();
    await reset.click();
    // URL очистился от genres, бейдж/счётчик пропал.
    await expect(page).not.toHaveURL(/sf_action/);
    await expect(page.getByRole('button', { name: 'Сбросить фильтры' })).toBeHidden();
  });

  test('бар поиска прилипает к верху при скролле', async ({ mockedPage: page }) => {
    await page.goto('/books');
    const input = page.getByPlaceholder('Поиск по названию или автору');
    await expect(input).toBeVisible({ timeout: 10_000 });
    // sticky-обёртка бара (родитель flex-строки) имеет position: sticky.
    const bar = page.locator('.sticky').filter({ has: input });
    await expect(bar).toHaveCSS('position', 'sticky');
  });

  test('бейдж на кнопке показывает число активных фильтров', async ({ mockedPage: page }) => {
    // Заходим сразу с активным фильтром в URL → бейдж = 1.
    await page.goto('/books?genres=sf_action');
    const filterBtn = page.getByRole('button', { name: 'Фильтры', exact: true });
    await expect(filterBtn).toBeVisible();
    await expect(filterBtn.getByText('1')).toBeVisible();
  });
});

test.describe('desktop /books', () => {
  test('sidebar виден, мобильная кнопка фильтров скрыта', async ({ mockedPage: page }) => {
    await page.goto('/books');
    // Постоянный sidebar-заголовок виден.
    await expect(page.getByRole('heading', { name: 'Фильтры' })).toBeVisible({
      timeout: 10_000,
    });
    // Кнопка-триггер drawer'а скрыта на десктопе (md:hidden).
    await expect(page.getByRole('button', { name: 'Фильтры', exact: true })).toBeHidden();
  });
});
