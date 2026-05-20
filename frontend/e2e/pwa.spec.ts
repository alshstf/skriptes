import { test, expect } from '@playwright/test';

/**
 * PWA smoke: проверяем что:
 *   1. /manifest.webmanifest доступен и содержит правильные поля.
 *   2. /sw.js доступен (service worker зарегистрирован vite-plugin-pwa).
 *   3. index.html ссылается на manifest + apple-touch-icon (иначе iOS
 *      Safari не покажет «Add to Home Screen»).
 *
 * Не используем `mockedPage` fixture: PWA-артефакты — статические файлы
 * из dist/, /api-моки тут не нужны и не должны мешать.
 */

test('pwa: /manifest.webmanifest serves correct fields', async ({ request }) => {
  const resp = await request.get('/manifest.webmanifest');
  expect(resp.status()).toBe(200);
  const ct = resp.headers()['content-type'] ?? '';
  // vite-plugin-pwa отдаёт application/manifest+json или application/json;
  // оба валидны для браузеров. Строгую строку не проверяем чтобы не
  // ломаться при обновлении плагина.
  expect(ct).toMatch(/json/);

  const manifest = await resp.json();
  expect(manifest.name).toBe('skriptes');
  expect(manifest.short_name).toBe('skriptes');
  expect(manifest.display).toBe('standalone');
  expect(manifest.start_url).toBe('/');
  expect(manifest.scope).toBe('/');
  // theme + background — должны совпадать (тёмная тема).
  expect(manifest.theme_color).toBe('#0a0a0a');
  expect(manifest.background_color).toBe('#0a0a0a');

  // Иконки: должна быть хотя бы одна 512×512 PNG (Chrome требует для
  // installability) и SVG для масштабирования. Не фиксируем точный
  // порядок — он не важен.
  expect(Array.isArray(manifest.icons)).toBe(true);
  const has512 = manifest.icons.some(
    (i: { sizes?: string; type?: string }) => i.sizes === '512x512' && i.type === 'image/png',
  );
  const hasSvg = manifest.icons.some((i: { type?: string }) => i.type === 'image/svg+xml');
  expect(has512).toBe(true);
  expect(hasSvg).toBe(true);
});

test('pwa: /sw.js is served', async ({ request }) => {
  const resp = await request.get('/sw.js');
  expect(resp.status()).toBe(200);
  expect(resp.headers()['content-type']).toMatch(/javascript/);
  const body = await resp.text();
  // Workbox-сгенерированный SW содержит характерные строки.
  expect(body).toContain('precache');
});

test('pwa: index.html links manifest + apple-touch-icon', async ({ request }) => {
  const resp = await request.get('/');
  expect(resp.status()).toBe(200);
  const html = await resp.text();
  expect(html).toContain('rel="manifest"');
  expect(html).toContain('href="/manifest.webmanifest"');
  expect(html).toContain('rel="apple-touch-icon"');
  expect(html).toContain('href="/apple-touch-icon.png"');
});
