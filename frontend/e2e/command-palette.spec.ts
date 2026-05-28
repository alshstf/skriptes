import { test, expect } from './_fixtures';

test('command palette: opens via ⌘K, shows three sections, navigates on click', async ({
  mockedPage: page,
}) => {
  await page.goto('/books');
  // Дожидаемся, что Layout отрендерился (палитра монтируется только под user).
  await expect(page.getByLabel('Открыть поиск')).toBeVisible({ timeout: 10_000 });

  // Открываем через Meta+K (Cmd на mac, Ctrl-mode под Linux на CI — оба
  // ловятся одним хендлером, который смотрит и metaKey и ctrlKey).
  await page.keyboard.press('Meta+K');

  const input = page.getByPlaceholder(/Поиск книг/);
  await expect(input).toBeVisible();

  // Подсказка пока не введено 2 символа.
  // exact: true чтобы не попасть на длинную sr-only DialogDescription.
  await expect(page.getByText('Введите минимум 2 символа', { exact: true })).toBeVisible();

  // Вводим — debounce 150ms + react-query → секции должны появиться.
  await input.fill('кад');

  // Скоупим поиск внутри dialog'а — на /books тоже есть "Кадетский корпус...".
  const dialog = page.getByRole('dialog');
  await expect(dialog.getByText('Книги', { exact: true })).toBeVisible();
  await expect(dialog.getByText('Авторы', { exact: true })).toBeVisible();
  await expect(dialog.getByText('Серии', { exact: true })).toBeVisible();
  await expect(dialog.getByText('Кадетский корпус. Книга 2')).toBeVisible();
  await expect(dialog.getByText('Кадет Иван')).toBeVisible();
  await expect(dialog.getByText('Кадетство')).toBeVisible();

  // Клик по книге → переход на /books/19 + закрытие палитры.
  await dialog.getByText('Кадетский корпус. Книга 2').click();
  await expect(page).toHaveURL(/\/books\/19$/);
  await expect(input).not.toBeVisible();
});

test('command palette: mobile — диалог в пределах экрана и прижат к верху', async ({
  mockedPage: page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/books');
  await expect(page.getByLabel('Открыть поиск')).toBeVisible({ timeout: 10_000 });

  // На мобильном hotkey не нажать — открываем кликом по триггеру.
  await page.getByLabel('Открыть поиск').click();
  const input = page.getByPlaceholder(/Поиск книг/);
  await expect(input).toBeVisible();
  await input.fill('кад');

  const dialog = page.getByRole('dialog');
  await expect(dialog.getByText('Кадетский корпус. Книга 2')).toBeVisible();

  // Диалог не должен вылезать за края экрана и должен быть прижат к верху
  // (а не центрирован — иначе результаты уходят под клавиатуру на iOS).
  const box = await dialog.boundingBox();
  expect(box).not.toBeNull();
  if (box) {
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(390);
    expect(box.y).toBeLessThanOrEqual(40);
  }
});
