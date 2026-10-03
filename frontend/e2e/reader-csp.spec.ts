import { test, expect } from '@playwright/test';

// Ридер под CSP (nginx-security-headers.conf; vite preview отдаёт ту же политику).
// foliate рендерит страницы книги в blob:-iframe'ах с sandbox="allow-same-origin
// allow-scripts" на нашем origin — без CSP скрипт внутри книги выполнился бы с
// правами сайта. Проверяем обе стороны: книга открывается и читается, а inline-
// скрипт, onerror= и <script src> из книги не выполняются.
//
// Используем чистый @playwright/test, а не mockedPage: там любое нарушение CSP —
// провал, а здесь нарушения и есть ожидаемый результат.

test('reader: книга открывается под CSP, скрипты из книги не выполняются', async ({ page }) => {
  const violations: string[] = [];
  page.on('console', (msg) => {
    if (msg.text().includes('Content Security Policy')) violations.push(msg.text());
  });
  await page.route('**/api/books/19/epub', (route) =>
    route.fulfill({ status: 200, contentType: 'application/epub+zip', body: probeEpub() }),
  );

  await page.goto(`/foliate-reader.html?src=${encodeURIComponent('/api/books/19/epub')}`);

  // Текст главы отрисован во вложенном iframe foliate → ридер работает под CSP.
  await expect
    .poll(
      async () => {
        for (const frame of page.frames()) {
          const text = await frame.evaluate(() => document.body?.innerText ?? '').catch(() => '');
          if (text.includes('CSP probe text')) return true;
        }
        return false;
      },
      { timeout: 15_000 },
    )
    .toBe(true);
  await expect(page.locator('#status')).toHaveCount(0);

  // Даём скриптам книги время сработать, если бы их пропустили.
  await page.waitForTimeout(500);
  const pwned = await page.evaluate(() => (window as unknown as { __pwned?: string[] }).__pwned ?? []);
  expect(pwned, 'скрипты из книги не должны выполняться').toEqual([]);
  // И не выполнились именно из-за CSP, а не потому что тест-книга сломана.
  expect(violations.some((v) => v.includes('script-src'))).toBe(true);
});

// ── тестовая книга ────────────────────────────────────────────────────────────

const mark = (via: string) => `window.top.__pwned=(window.top.__pwned||[]).concat('${via}')`;

function probeEpub(): Buffer {
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
    <dc:identifier id="uid">csp-probe</dc:identifier>
    <dc:title>CSP probe</dc:title>
    <dc:language>ru</dc:language>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml" properties="scripted"/>
    <item id="evil" href="evil.js" media-type="application/javascript"/>
  </manifest>
  <spine><itemref idref="ch1"/></spine>
</package>`,
    ],
    [
      'OEBPS/nav.xhtml',
      `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<head><title>nav</title></head>
<body><nav epub:type="toc"><ol><li><a href="ch1.xhtml">ch1</a></li></ol></nav></body>
</html>`,
    ],
    [
      'OEBPS/ch1.xhtml',
      `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>ch1</title><script src="evil.js"></script></head>
<body>
<p>CSP probe text</p>
<script>${mark('inline')}</script>
<img src="missing.png" alt="" onerror="${mark('onerror')}"/>
</body>
</html>`,
    ],
    ['OEBPS/evil.js', `${mark('script-src')};`],
  ]);
}

// Минимальный ZIP без сжатия (method 0 — «stored»): EPUB требует, чтобы mimetype
// лежал первым и несжатым, а остальное foliate (zip.js) читает и так.
function zipStored(files: [string, string][]): Buffer {
  const locals: Buffer[] = [];
  const centrals: Buffer[] = [];
  let offset = 0;
  for (const [name, content] of files) {
    const nameBuf = Buffer.from(name, 'utf8');
    const data = Buffer.from(content, 'utf8');
    const crc = crc32(data);

    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(20, 4); // version needed
    local.writeUInt32LE(crc, 14);
    local.writeUInt32LE(data.length, 18);
    local.writeUInt32LE(data.length, 22);
    local.writeUInt16LE(nameBuf.length, 26);
    locals.push(local, nameBuf, data);

    const central = Buffer.alloc(46);
    central.writeUInt32LE(0x02014b50, 0);
    central.writeUInt16LE(20, 4); // version made by
    central.writeUInt16LE(20, 6); // version needed
    central.writeUInt32LE(crc, 16);
    central.writeUInt32LE(data.length, 20);
    central.writeUInt32LE(data.length, 24);
    central.writeUInt16LE(nameBuf.length, 28);
    central.writeUInt32LE(offset, 42);
    centrals.push(central, nameBuf);

    offset += local.length + nameBuf.length + data.length;
  }
  const cd = Buffer.concat(centrals);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(files.length, 8);
  end.writeUInt16LE(files.length, 10);
  end.writeUInt32LE(cd.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...locals, cd, end]);
}

function crc32(buf: Buffer): number {
  let crc = 0xffffffff;
  for (const byte of buf) {
    crc ^= byte;
    for (let k = 0; k < 8; k++) crc = crc & 1 ? (crc >>> 1) ^ 0xedb88320 : crc >>> 1;
  }
  return (crc ^ 0xffffffff) >>> 0;
}
