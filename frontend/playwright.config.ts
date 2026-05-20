import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright e2e ловит то, что unit-тесты в jsdom не могут — реальный
 * рендер CSS, реальные размеры элементов, layout-регрессии.
 *
 * Тесты запускаются на собранном статическом билде (`vite preview`):
 * это близко к продакшену и не требует поднятого backend — все
 * /api/* запросы стабятся через page.route() в самих тестах.
 */
export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: [['list']],
  use: {
    baseURL: 'http://localhost:4173',
    trace: 'on-first-retry',
    // Блокируем service worker дефолтно. SW из vite-plugin-pwa
    // (sw.js + runtime caching) интерферирует с page.route() моками:
    // кэширует первый ответ /api/books/{id} и подменяет последующие,
    // ломая polling-тесты (covers/annotation/author-bio и др.). PWA-
    // специфичные проверки (e2e/pwa.spec.ts) переопределяют это
    // локально через test.use({ serviceWorkers: 'allow' }).
    serviceWorkers: 'block',
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
  ],
  webServer: {
    command: 'npm run preview -- --strictPort --port=4173',
    url: 'http://localhost:4173',
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
  },
});
