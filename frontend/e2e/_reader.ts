import { expect, type Frame, type Page } from '@playwright/test';
import { zipStored } from './_epub';

// Ридер в e2e: длинная тестовая книга (две главы по ~10 страниц на телефоне),
// открытие с заданной сохранённой позицией и где мы сейчас.

const PARA =
  'Он шёл по пустыне, и песок скрипел под ногами, а солнце стояло высоко, и тени не было нигде, ' +
  'кроме как под камнями, где прятались ящерицы и прочая мелкая живность пустыни. ';

function chapter(n: number): string {
  // В первом абзаце — ссылка-сноска: на неё тапают, и она остаётся в фокусе (#591).
  const ps = Array.from(
    { length: 40 },
    (_, i) =>
      `<p id="p${n}-${i + 1}">${n}.${i + 1}. ${i === 0 ? `<a id="fn${n}" href="#p${n}-40">[${n}]</a> ` : ''}${PARA.repeat(2)}</p>`,
  ).join('');
  return `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" lang="ru"><head><title>ch${n}</title></head>
<body><h1>Глава ${n}</h1>${ps}</body></html>`;
}

export function longEpub(): Buffer {
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
    <dc:identifier id="uid">position</dc:identifier><dc:title>Пустыня</dc:title><dc:language>ru</dc:language>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="ch2" href="ch2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="ch1"/><itemref idref="ch2"/></spine>
</package>`,
    ],
    [
      'OEBPS/nav.xhtml',
      `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<head><title>nav</title></head>
<body><nav epub:type="toc"><ol><li><a href="ch1.xhtml">Глава 1</a></li><li><a href="ch2.xhtml">Глава 2</a></li></ol></nav></body>
</html>`,
    ],
    ['OEBPS/ch1.xhtml', chapter(1)],
    ['OEBPS/ch2.xhtml', chapter(2)],
  ]);
}

export const BOOK_ID = 19;

/** Сохранённые ридером позиции (тела PUT /position) — по порядку. */
export type SavedPositions = { pos: string; fraction?: number }[];

/**
 * Стабы ридера: книга, позиция (GET отдаёт serverPos — функцию, чтобы тест мог
 * её менять), заметки. Возвращает журнал принятых PUT /position; failSaves —
 * сохранения не доходят до сервера (сетевой сбой).
 */
export async function stubReader(
  page: Page,
  serverPos: () => string = () => '',
  opts: { failSaves?: boolean } = {},
): Promise<SavedPositions> {
  const saved: SavedPositions = [];
  await page.route(`**/api/books/${BOOK_ID}/epub`, (route) =>
    route.fulfill({ status: 200, contentType: 'application/epub+zip', body: longEpub() }),
  );
  await page.route(new RegExp(`/api/books/${BOOK_ID}/position$`), (route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ pos: serverPos() }) });
    }
    if (opts.failSaves) return route.abort('failed');
    saved.push(route.request().postDataJSON());
    return route.fulfill({ status: 204 });
  });
  await page.route(new RegExp(`/api/books/${BOOK_ID}/annotations$`), (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '{"items":[]}' }),
  );
  return saved;
}

export function readerFrame(page: Page): Frame | null {
  return page.frames().find((f) => f.url().includes('/foliate-reader.html')) ?? null;
}

/** Где мы: секция (глава) и страница в ней (у foliate 0 и последняя — пустые). */
export async function location(page: Page): Promise<{ index: number; page: number } | null> {
  const frame = readerFrame(page);
  if (!frame) return null;
  return frame
    .evaluate(() => {
      const view = document.querySelector('foliate-view') as unknown as {
        renderer?: { page: number; getContents(): { index: number }[] };
      } | null;
      const r = view?.renderer;
      const c = r?.getContents?.()[0];
      return r && c ? { index: c.index, page: r.page } : null;
    })
    .catch(() => null);
}

/** Ридер открыт и стоит на странице `at`. */
export async function waitReaderAt(page: Page, at = { index: 0, page: 1 }) {
  await expect.poll(() => readerFrame(page) !== null).toBe(true);
  await expect.poll(() => location(page), { timeout: 15_000 }).toEqual(at);
}

/** Открыть ридер и дождаться страницы `at`. */
export async function openReader(page: Page, at = { index: 0, page: 1 }) {
  await page.goto(`/books/${BOOK_ID}/read`);
  await waitReaderAt(page, at);
}

/** Документ главы (blob-iframe внутри ридера) с заголовком «Глава n». */
export async function chapterDoc(page: Page, n = 1): Promise<Frame> {
  let found: Frame | null = null;
  await expect
    .poll(async () => {
      for (const f of page.frames()) {
        const ok = await f.evaluate((t) => document.querySelector('h1')?.textContent === t, `Глава ${n}`).catch(() => false);
        if (ok) {
          found = f;
          return true;
        }
      }
      return false;
    })
    .toBe(true);
  return found!;
}
