import { test, expect } from './_fixtures';

test('author page: books are grouped under their series + Вне серий section', async ({
  mockedPage: page,
}) => {
  await page.goto('/authors/17');

  // Заголовок секции серии — кликабельный, ведёт на /series/7.
  const seriesLink = page.getByRole('link', { name: 'Петля [Алексеев]' });
  await expect(seriesLink).toBeVisible({ timeout: 10_000 });
  await expect(seriesLink).toHaveAttribute('href', '/series/7');

  // Badge "СЕРИЯ" рядом с названием — явный визуальный маркер
  // вместо иконки.
  await expect(page.getByText('Серия', { exact: true })).toBeVisible();

  // Книги серии — обе видны на странице автора.
  await expect(page.getByText('Кадетский корпус. Книга 1')).toBeVisible();
  await expect(page.getByText('Кадетский корпус. Книга 2')).toBeVisible();

  // Номера томов в серии: aria-label "Том 1" / "Том 2".
  await expect(page.getByLabel('Том 1')).toBeVisible();
  await expect(page.getByLabel('Том 2')).toBeVisible();

  // Псевдосекция "Вне серий" + книга в ней.
  await expect(page.getByText('Вне серий')).toBeVisible();
  await expect(page.getByText('Отдельный роман')).toBeVisible();
});
