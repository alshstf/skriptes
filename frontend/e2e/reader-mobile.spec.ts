import { test, expect } from './_fixtures';
import { zipStored } from './_epub';
import type { Frame, Page } from '@playwright/test';

// Ридер на телефоне: страница на весь экран, панель — оверлей по тапу в центр,
// перелистывание тапом по краям и свайпом (каждое отключается в настройках),
// неблокирующая анимация (быстрые тапы не теряются), тема страницы.

test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });

const W = 390;
const H = 844;

const PARA =
  'Он шёл по пустыне, и песок скрипел под ногами, а солнце стояло высоко, и тени не было нигде, ' +
  'кроме как под камнями, где прятались ящерицы и прочая мелкая живность пустыни. ';

function chapter(n: number): string {
  const ps = Array.from({ length: 40 }, (_, i) => `<p>${n}.${i + 1}. ${PARA.repeat(2)}</p>`).join('');
  return `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" lang="ru"><head><title>ch${n}</title></head>
<body><h1>Глава ${n}</h1>${ps}</body></html>`;
}

function longEpub(): Buffer {
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
    <dc:identifier id="uid">mobile</dc:identifier><dc:title>Пустыня</dc:title><dc:language>ru</dc:language>
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

async function openReader(page: Page) {
  await page.route('**/api/books/19/epub', (route) =>
    route.fulfill({ status: 200, contentType: 'application/epub+zip', body: longEpub() }),
  );
  await page.route(/\/api\/books\/19\/position$/, (route) =>
    route.request().method() === 'GET'
      ? route.fulfill({ status: 200, contentType: 'application/json', body: '{"pos":""}' })
      : route.fulfill({ status: 204 }),
  );
  await page.route(/\/api\/books\/19\/annotations$/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '{"items":[]}' }),
  );
  await page.goto('/books/19/read');
  await expect.poll(() => readerFrame(page) !== null).toBe(true);
  // Книга открыта: первая страница первой главы.
  await expect.poll(() => location(page), { timeout: 15_000 }).toEqual({ index: 0, page: 1 });
}

function readerFrame(page: Page): Frame | null {
  return page.frames().find((f) => f.url().includes('/foliate-reader.html')) ?? null;
}

/** Где мы: секция (глава) и страница в ней (у foliate 0 и последняя — пустые). */
async function location(page: Page): Promise<{ index: number; page: number } | null> {
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

/** Документ главы (blob-iframe внутри ридера) — для стилей страницы. */
async function bookDoc(page: Page): Promise<Frame> {
  let found: Frame | null = null;
  await expect
    .poll(async () => {
      for (const f of page.frames()) {
        const ok = await f.evaluate(() => document.querySelector('h1')?.textContent?.startsWith('Глава')).catch(() => false);
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

async function swipe(page: Page, fromX: number, toX: number) {
  const cdp = await page.context().newCDPSession(page);
  const y = H / 2;
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: fromX, y }] });
  const steps = 8;
  for (let i = 1; i <= steps; i++) {
    const x = fromX + ((toX - fromX) * i) / steps;
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x, y }] });
  }
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
  await cdp.detach();
}

const header = (page: Page) => page.locator('header').filter({ has: page.getByRole('button', { name: 'Настройки чтения' }) });

test('ридер на телефоне: страница во весь экран, панель — по тапу в центр', async ({ mockedPage: page }) => {
  await openReader(page);

  // Страница книги — на весь экран (раньше сверху были две панели).
  const box = await page.locator('iframe[title="Foliate reader"]').boundingBox();
  expect(box).toMatchObject({ x: 0, y: 0, width: W, height: H });

  // Панель видна при открытии; тап по центру прячет, ещё один — показывает.
  await expect(header(page)).not.toHaveAttribute('inert');
  await page.touchscreen.tap(W / 2, H / 2);
  await expect(header(page)).toHaveAttribute('inert', '');
  await page.touchscreen.tap(W / 2, H / 2);
  await expect(header(page)).not.toHaveAttribute('inert');
  // Тап по центру страницу не листает.
  expect(await location(page)).toEqual({ index: 0, page: 1 });
});

test('тап по краям листает и прячет панель; быстрые тапы не теряются', async ({ mockedPage: page }) => {
  await openReader(page);

  await page.touchscreen.tap(W - 30, H / 2);
  await expect.poll(() => location(page)).toEqual({ index: 0, page: 2 });
  await expect(header(page)).toHaveAttribute('inert', '');

  await page.touchscreen.tap(30, H / 2);
  await expect.poll(() => location(page)).toEqual({ index: 0, page: 1 });

  // Три тапа подряд, не дожидаясь анимации, — три страницы.
  await page.touchscreen.tap(W - 30, H / 2);
  await page.touchscreen.tap(W - 30, H / 2);
  await page.touchscreen.tap(W - 30, H / 2);
  await expect.poll(() => location(page)).toEqual({ index: 0, page: 4 });
});

test('свайп листает; выключенные в настройках свайп и тапы — нет', async ({ mockedPage: page }) => {
  await openReader(page);

  await swipe(page, W - 40, 60);
  await expect.poll(() => location(page)).toEqual({ index: 0, page: 2 });
  await swipe(page, 60, W - 40);
  await expect.poll(() => location(page)).toEqual({ index: 0, page: 1 });

  // Выключаем свайп.
  await page.touchscreen.tap(W / 2, H / 2);
  await page.getByRole('button', { name: 'Настройки чтения' }).click();
  const sheet = page.getByRole('dialog', { name: 'Настройки чтения' });
  await sheet.getByRole('switch', { name: /Свайпом/ }).click();
  await expect(sheet.getByRole('switch', { name: /Свайпом/ })).not.toBeChecked();
  // Последний способ выключить нельзя.
  await expect(sheet.getByRole('switch', { name: /Тапом по краям/ })).toBeDisabled();
  await page.keyboard.press('Escape');
  await expect(sheet).toBeHidden();

  await swipe(page, W - 40, 60);
  await page.waitForTimeout(400);
  expect(await location(page)).toEqual({ index: 0, page: 1 });
  await page.touchscreen.tap(W - 30, H / 2);
  await expect.poll(() => location(page)).toEqual({ index: 0, page: 2 });

  // Свайп обратно включаем, тапы выключаем: тап по краю — только меню.
  await page.touchscreen.tap(W / 2, H / 2);
  await page.getByRole('button', { name: 'Настройки чтения' }).click();
  await sheet.getByRole('switch', { name: /Свайпом/ }).click();
  await sheet.getByRole('switch', { name: /Тапом по краям/ }).click();
  await page.keyboard.press('Escape');
  await expect(sheet).toBeHidden();
  await expect(header(page)).not.toHaveAttribute('inert');
  await page.touchscreen.tap(W - 30, H / 2);
  await expect(header(page)).toHaveAttribute('inert', '');
  await page.waitForTimeout(400);
  expect(await location(page)).toEqual({ index: 0, page: 2 });

  // Настройки — на устройстве.
  const stored = await page.evaluate(() => JSON.parse(localStorage.getItem('skriptes.reader') ?? '{}'));
  expect(stored).toMatchObject({ swipe: true, tapZones: false });
});

test('iOS PWA: страница и панель — ниже системного размытия под статус-баром', async ({ mockedPage: page }) => {
  // iOS 26+ размывает полосу ~20pt под статус-баром (скриншот владельца, iOS 27):
  // заголовок панели на inset+6 был размыт. Инсеты iPhone эмулируем через CDP.
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('Emulation.setSafeAreaInsetsOverride' as never, { insets: { top: 59, bottom: 34 } } as never);
  await openReader(page);
  const top = (sel: string) => page.locator(sel).first().evaluate((el) => el.getBoundingClientRect().top);
  const BAND = 59 + 20;
  expect(await top('iframe[title="Foliate reader"]')).toBeGreaterThanOrEqual(BAND);
  expect(await top('header .truncate')).toBeGreaterThanOrEqual(BAND);
  // Шторки (ui/sheet) — то же: заголовок «Заметки» ниже полосы.
  await page.getByRole('button', { name: 'Заметки и закладки' }).click();
  const title = page.getByRole('dialog', { name: 'Заметки' }).getByRole('heading', { name: 'Заметки' });
  await expect(title).toBeVisible();
  expect(await title.evaluate((el) => el.getBoundingClientRect().top)).toBeGreaterThanOrEqual(BAND);
});

test('тема страницы: тёмная по умолчанию, светлая и шрифт — из настроек', async ({ mockedPage: page }) => {
  await openReader(page);
  const doc = await bookDoc(page);
  const look = () =>
    doc.evaluate(() => {
      const b = getComputedStyle(document.body);
      return { bg: b.backgroundColor, fg: b.color, size: getComputedStyle(document.documentElement).fontSize };
    });
  expect(await look()).toMatchObject({ bg: 'rgb(20, 20, 20)', fg: 'rgb(208, 208, 208)' });
  const baseSize = parseFloat((await look()).size);

  await page.getByRole('button', { name: 'Настройки чтения' }).click();
  const sheet = page.getByRole('dialog', { name: 'Настройки чтения' });
  await sheet.getByRole('button', { name: 'Светлая' }).click();
  await expect.poll(async () => (await look()).bg).toBe('rgb(255, 255, 255)');
  await sheet.getByRole('button', { name: 'Шрифт крупнее' }).click();
  await expect(sheet.getByText('110 %')).toBeVisible();
  await expect.poll(async () => parseFloat((await look()).size)).toBeCloseTo(baseSize * 1.1, 1);

  // После перезагрузки — та же тема (localStorage), сразу из URL iframe.
  await page.reload();
  const doc2 = await bookDoc(page);
  await expect
    .poll(() => doc2.evaluate(() => getComputedStyle(document.body).backgroundColor).catch(() => ''))
    .toBe('rgb(255, 255, 255)');
});
