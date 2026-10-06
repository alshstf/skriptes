import { test, expect } from './_fixtures';

// Готовые подборки (#389): блок «Подборки» над личными полками, подборка
// раскрывается и показывает книги с пояснением; пустая — не раскрывается.

const presets = [
  { key: 'unfinished-series', title: 'Недочитанные серии', hint: 'Следующая книга в сериях, которые вы начали', count: 1 },
  { key: 'read-this-year', title: 'Прочитано в 2026 году', hint: 'Книги, отмеченные прочитанными в этом году', count: 0 },
];

test('подборки: список, раскрытие, пояснение, пустая неактивна', async ({ mockedPage: page }) => {
  await page.route(/\/api\/me\/collections$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [] }) }),
  );
  await page.route(/\/api\/me\/presets$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: presets }) }),
  );
  await page.route(/\/api\/me\/presets\/unfinished-series$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [{ id: 301, title: 'Дети Дюны', authors: ['Герберт Фрэнк'], series: 'Хроники Дюны', lib_id: '' }],
        notes: { '301': 'в серии прочитано 2 из 6' },
      }),
    }),
  );
  await page.goto('/shelves');
  const section = page.getByRole('region', { name: 'Подборки' });
  await expect(section).toBeVisible({ timeout: 10_000 });

  const empty = section.getByRole('button', { name: /Прочитано в 2026 году/ });
  await expect(empty).toBeDisabled();

  await section.getByRole('button', { name: /Недочитанные серии/ }).click();
  await expect(section.getByRole('link', { name: /Дети Дюны/ })).toHaveAttribute('href', '/works/301');
  await expect(section.getByText('в серии прочитано 2 из 6')).toBeVisible();
});
