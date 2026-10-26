import { test, expect } from '@playwright/test';

// Service worker собранного билда и данные /api (#609): позиция чтения и
// заметки идут мимо его кэша, данные каталога — NetworkFirst (в сети свежие,
// кэш — оффлайн). Прежний StaleWhileRevalidate отдавал ответ ПРОШЛОГО запроса —
// книга открывалась там, где её открывали в прошлый раз.
//
// Приложение на localhost SW не регистрирует (lib/pwa.ts) — регистрируем сами
// со страницы ридера (статика в scope SW, без /api-моков приложения).

test.use({ serviceWorkers: 'allow' });

test('pwa: позиция и заметки — из сети, данные каталога — свежие, оффлайн — из кэша', async ({ page, context }) => {
  let n = 0;
  let offline = false;
  await context.route(/\/api\/books\/19(\/position|\/annotations)?$/, (route) => {
    if (offline) return route.abort('internetdisconnected');
    n += 1;
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ n }) });
  });

  await page.goto('/foliate-reader.html');
  await page.evaluate(async () => {
    await navigator.serviceWorker.register('/sw.js');
    await navigator.serviceWorker.ready;
  });
  await expect.poll(() => page.evaluate(() => navigator.serviceWorker.controller !== null), { timeout: 15_000 }).toBe(true);

  const get = (url: string) =>
    page.evaluate(async (u) => {
      try {
        const r = await fetch(u);
        return r.ok ? ((await r.json()) as { n: number }).n : r.status;
      } catch {
        return 'network-error';
      }
    }, url);

  // Каждый запрос — новый ответ сервера, а не прошлый из кэша.
  expect(await get('/api/books/19/position')).toBe(1);
  expect(await get('/api/books/19/position')).toBe(2);
  expect(await get('/api/books/19/annotations')).toBe(3);
  expect(await get('/api/books/19/annotations')).toBe(4);
  expect(await get('/api/books/19')).toBe(5);
  expect(await get('/api/books/19')).toBe(6);

  // Оффлайн: карточка — последняя увиденная; позиции из кэша SW нет (её
  // отдаст копия ридера на устройстве).
  offline = true;
  expect(await get('/api/books/19')).toBe(6);
  expect(await get('/api/books/19/position')).toBe('network-error');
});
