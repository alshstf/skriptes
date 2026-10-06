import { test, expect, bookDetailFixture } from './_fixtures';
import { zipStored } from './_epub';
import type { Frame, Page } from '@playwright/test';

// Закладки и выделения с заметками (#389): выделение текста в странице книги →
// полоса действий → «Выделить» сохраняет подсветку; закладка — кнопкой; список в
// «Заметках»; «Мои заметки» на карточке ведут в ридер на место.

const TEXT = 'Страх убивает разум. Страх — это малая смерть.';

function bookEpub(): Buffer {
  return zipStored([
    ['mimetype', 'application/epub+zip'],
    [
      'META-INF/container.xml',
      `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`,
    ],
    [
      'OEBPS/content.opf',
      `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">annotations</dc:identifier><dc:title>Дюна</dc:title><dc:language>ru</dc:language>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="ch1"/></spine>
</package>`,
    ],
    [
      'OEBPS/nav.xhtml',
      `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<head><title>nav</title></head>
<body><nav epub:type="toc"><ol><li><a href="ch1.xhtml">Литания</a></li></ol></nav></body>
</html>`,
    ],
    [
      'OEBPS/ch1.xhtml',
      `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>ch1</title></head>
<body><p id="p1">${TEXT}</p></body></html>`,
    ],
  ]);
}

async function bookFrame(page: Page): Promise<Frame> {
  let found: Frame | null = null;
  await expect
    .poll(
      async () => {
        for (const frame of page.frames()) {
          const text = await frame.evaluate(() => document.body?.innerText ?? '').catch(() => '');
          if (text.includes('Страх убивает разум')) {
            found = frame;
            return true;
          }
        }
        return false;
      },
      { timeout: 15_000 },
    )
    .toBe(true);
  return found!;
}

test('ридер: выделение → подсветка, закладка, список заметок', async ({ mockedPage: page }) => {
  let items: Record<string, unknown>[] = [];
  const posted: Record<string, unknown>[] = [];
  await page.route('**/api/books/19/epub', (route) =>
    route.fulfill({ status: 200, contentType: 'application/epub+zip', body: bookEpub() }),
  );
  await page.route(/\/api\/books\/19\/position$/, (route) =>
    route.request().method() === 'GET'
      ? route.fulfill({ status: 200, contentType: 'application/json', body: '{"pos":""}' })
      : route.fulfill({ status: 204 }),
  );
  await page.route(/\/api\/books\/19\/annotations$/, async (route) => {
    if (route.request().method() === 'POST') {
      const body = route.request().postDataJSON() as Record<string, unknown>;
      posted.push(body);
      const saved = { id: posted.length, book_id: 19, created_at: '2026-10-07T00:00:00Z', updated_at: '2026-10-07T00:00:00Z', ...body };
      items = [...items, saved];
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(saved) });
      return;
    }
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items }) });
  });

  await page.goto('/books/19/read');
  const frame = await bookFrame(page);

  // Выделяем «Страх убивает разум.» и отпускаем указатель — как пользователь.
  await frame.evaluate(() => {
    const p = document.getElementById('p1')!;
    const range = document.createRange();
    range.setStart(p.firstChild!, 0);
    range.setEnd(p.firstChild!, 'Страх убивает разум.'.length);
    const sel = document.getSelection()!;
    sel.removeAllRanges();
    sel.addRange(range);
    document.dispatchEvent(new PointerEvent('pointerup', { bubbles: true }));
  });
  const bar = page.getByRole('toolbar', { name: 'Выделенный текст' });
  await expect(bar).toContainText('Страх убивает разум.');
  await bar.getByRole('button', { name: 'Выделить' }).click();
  await expect.poll(() => posted.length).toBe(1);
  expect(posted[0]).toMatchObject({ kind: 'highlight', excerpt: 'Страх убивает разум.' });
  expect(String(posted[0].cfi)).toMatch(/^epubcfi\(/);
  await expect(bar).toHaveCount(0);

  await page.getByRole('button', { name: 'Закладка на этой странице' }).click();
  await expect.poll(() => posted.length).toBe(2);
  expect(posted[1]).toMatchObject({ kind: 'bookmark' });
  await expect(page.getByRole('button', { name: 'Убрать закладку' })).toBeVisible();

  await page.getByRole('button', { name: 'Заметки и закладки' }).click();
  const sheet = page.getByRole('dialog', { name: 'Заметки' });
  await expect(sheet.getByText('«Страх убивает разум.»')).toBeVisible();
  await expect(sheet.getByText(/^Закладка/)).toBeVisible();
});

test('карточка: «Мои заметки» ведут в ридер на место', async ({ mockedPage: page }) => {
  await page.route(/\/api\/works\/\d+$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ...bookDetailFixture, work_id: 501 }) }),
  );
  await page.route(/\/api\/works\/501\/annotations$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        items: [
          { id: 1, book_id: 19, kind: 'highlight', cfi: 'epubcfi(/6/2!/4/2,/1:0,/1:20)', excerpt: 'Страх убивает разум.',
            note: 'литания', label: 'Литания', fraction: 0.4, created_at: '', updated_at: '' },
        ],
      }),
    }),
  );
  await page.goto('/works/501');
  const section = page.getByRole('region', { name: /Мои заметки/ });
  await expect(section).toBeVisible({ timeout: 10_000 });
  await expect(section.getByText('литания', { exact: true })).toBeVisible();
  await expect(section.getByRole('link')).toHaveAttribute('href', /\/books\/19\/read\?cfi=epubcfi/);
});
