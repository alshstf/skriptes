import { test, expect } from './_fixtures';

/**
 * Регрессионный тест против бага из v0.4: внутри DropdownMenu пункт
 * "EPUB 3" разъезжался на две строки потому что layout был flex-row
 * с длинным описанием справа — label сжимался и переносился.
 *
 * Что проверяем:
 *   1. меню открывается и ВСЕ форматы видны
 *   2. короткие label'ы (EPUB 3, EPUB 2, KEPUB, AZW8, KFX, FB2) — на одной
 *      строке. Меряем по высоте: одна строка должна быть < 28px шрифтом
 *      по умолчанию (font-medium ~ 16px line-height). Сделаем порог 30px
 *      чтобы был запас для разных font metrics.
 *   3. длинные описания МОГУТ переноситься (они под label'ом, это ок).
 */
test('DownloadMenu: format labels never wrap to multiple lines', async ({ mockedPage: page }) => {
  await page.goto('/books/19');

  // Кнопка "Скачать" появляется только когда детали загружены.
  await expect(page.getByRole('button', { name: 'Скачать' })).toBeVisible();
  await page.getByRole('button', { name: 'Скачать' }).click();

  // Меню открыто — должны быть все 6 форматов.
  for (const label of ['EPUB 3', 'EPUB 2', 'KEPUB', 'AZW8', 'KFX', 'FB2']) {
    const node = page.getByText(label, { exact: true });
    await expect(node).toBeVisible();

    const box = await node.boundingBox();
    expect(box, `label '${label}' must have a bounding box`).not.toBeNull();
    // Однострочный label при font-medium ~ 16px line-height ≈ 24px.
    // Ставим порог 30px: достаточно строго чтобы поймать перенос на 2
    // строки (≈ 48px), но щадяще к разным font metrics.
    expect(box!.height).toBeLessThan(30);
  }
});

test('DownloadMenu: menu items have proper download href', async ({ mockedPage: page }) => {
  await page.goto('/books/19');
  await page.getByRole('button', { name: 'Скачать' }).click();

  // DropdownMenuItem рендерится с asChild, поэтому role=menuitem уже на
  // самом <a>. Атрибуты проверяем прямо на нём, без вложенного locator.
  const epub3 = page.getByRole('menuitem').filter({ hasText: 'EPUB 3' });
  await expect(epub3).toBeVisible();
  await expect(epub3).toHaveAttribute('href', '/api/books/19/download?format=epub3');
  await expect(epub3).toHaveAttribute('download', '');
});
