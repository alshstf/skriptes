import { test, expect } from './_fixtures';

// #469: «Назад» с карточки книги возвращает в ту же раскрытую подборку на ту же
// позицию прокрутки (раскрытие — в адресе ?open=…, прокрутку восстанавливает
// роутер), а на странице премии сохраняется «Только из библиотеки».

const many = Array.from({ length: 40 }, (_, i) => ({
  id: 400 + i,
  work_id: 400 + i,
  title: `Книга подборки ${i + 1}`,
  authors: ['Автор Тестовый'],
  lib_id: '',
}));

test('подборка: «Назад» с карточки — подборка раскрыта, прокрутка та же', async ({ mockedPage: page }) => {
  await page.route(/\/api\/me\/collections$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [] }) }),
  );
  await page.route(/\/api\/me\/presets$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ items: [{ key: 'adaptations', title: 'Экранизации', hint: 'скоро', count: 40 }] }),
    }),
  );
  await page.route(/\/api\/me\/presets\/adaptations$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: many, notes: {} }) }),
  );
  await page.setViewportSize({ width: 390, height: 700 });
  await page.goto('/shelves');
  await page.getByRole('button', { name: /Экранизации/ }).click();
  await expect(page).toHaveURL(/open=/);
  const target = page.getByRole('link', { name: /Книга подборки 30 / });
  await target.scrollIntoViewIfNeeded();
  const before = await page.evaluate(() => window.scrollY);
  expect(before).toBeGreaterThan(500);

  await target.click();
  await expect(page).toHaveURL(/\/works\/429/);
  await page.goBack();
  await expect(page.getByRole('link', { name: /Книга подборки 30 / })).toBeVisible();
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(before - 40);
  expect(await page.evaluate(() => window.scrollY)).toBeLessThan(before + 40);
});

test('премия: «Только из библиотеки» в адресе', async ({ mockedPage: page }) => {
  await page.route(/\/api\/awards\/hugo$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        award: { key: 'hugo', name: 'Хьюго', group: 'Международные' },
        wins: [
          { id: 1, year: 2020, nomination: 'Роман', kind: 'work', title: 'Есть в библиотеке', author: 'А', source: 'fantlab', item: { ...many[0], title: 'Есть в библиотеке' } },
          { id: 2, year: 2020, nomination: 'Роман', kind: 'work', title: 'Нет в библиотеке', author: 'Б', source: 'fantlab' },
        ],
      }),
    }),
  );
  await page.goto('/awards/hugo');
  await expect(page.getByText('Нет в библиотеке')).toBeVisible({ timeout: 10_000 });
  await page.getByRole('switch', { name: 'Только книги из библиотеки' }).click();
  await expect(page).toHaveURL(/library=true/);
  await expect(page.getByText('Нет в библиотеке')).toHaveCount(0);
});
