import { test, expect } from './_fixtures';
import { openReader, stubReader } from './_reader';

// iOS 26 (WebKit 301108): установленное приложение получает окно короче экрана
// на высоту статус-бара, пока документ короче экрана, — правило html
// min-height в index.css (display-mode: standalone). Ни сам баг, ни
// standalone Chromium не воспроизводит; здесь — что правило доехало до
// собранного CSS и с инсетами iPhone не делает страницу прокручиваемой.

test.use({ viewport: { width: 393, height: 852 }, hasTouch: true, isMobile: true });

test('правило iOS 26 для установленного приложения — в сборке и без прокрутки', async ({ mockedPage: page }) => {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('Emulation.setSafeAreaInsetsOverride' as never, { insets: { top: 59, bottom: 34 } } as never);
  await stubReader(page);
  await openReader(page);
  const rule = await page.evaluate(() => {
    for (const sheet of Array.from(document.styleSheets)) {
      for (const r of Array.from(sheet.cssRules)) {
        if (r instanceof CSSMediaRule && r.conditionText.includes('display-mode: standalone')) {
          for (const inner of Array.from(r.cssRules)) {
            if (inner instanceof CSSStyleRule && inner.selectorText === 'html') return inner.style.minHeight;
          }
        }
      }
    }
    return '';
  });
  expect(rule).toContain('100lvh');
  expect(rule).toContain('safe-area-inset-top');
  // Обычная вкладка — правило не действует.
  expect(await page.evaluate(() => getComputedStyle(document.documentElement).minHeight)).toBe('0px');
  // Как в установленном приложении: то же значение на html — страница не длиннее окна.
  const scroll = await page.evaluate((v) => {
    document.documentElement.style.minHeight = v;
    return document.documentElement.scrollHeight - innerHeight;
  }, rule);
  expect(scroll).toBeLessThanOrEqual(0);
});
