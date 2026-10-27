import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import { VitePWA } from 'vite-plugin-pwa';
import path from 'node:path';
import fs from 'node:fs';

// CSP — единственный источник правды nginx-security-headers.conf (его include'ит
// nginx прод-образа). Здесь — тот же заголовок для `vite preview`, на котором
// гоняются Playwright e2e: нарушение CSP в приложении или ридере ловится тестами.
// В dev-сервер НЕ ставим: Vite dev вставляет inline-скрипт React Refresh.
function readCSP(): string {
  const conf = fs.readFileSync(path.resolve(__dirname, 'nginx-security-headers.conf'), 'utf8');
  const m = conf.match(/^add_header Content-Security-Policy "([^"]+)" always;$/m);
  if (!m) throw new Error('nginx-security-headers.conf: не найден add_header Content-Security-Policy');
  return m[1];
}

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    // ── PWA: service worker + web app manifest ──────────────────────
    // registerType=autoUpdate — новый SW активируется сразу после
    // скачивания (skipWaiting + clientsClaim). Страницу под новую сборку
    // перезагружает lib/pwa.ts::onNewVersion — в фоне сразу, на экране при
    // переходе на другую страницу или по кнопке в тосте, не посреди ввода (#526).
    //
    // injectRegister=null — регистрируем SW вручную в src/main.tsx
    // (через virtual:pwa-register). Это даёт точку контроля: показать
    // toast о новой версии, отложить активацию и т.п. Стандартная
    // auto-inject пишет тег в index.html напрямую, без хука.
    VitePWA({
      registerType: 'autoUpdate',
      injectRegister: null,
      includeAssets: ['icon.svg', 'apple-touch-icon.png', 'favicon-32.png'],
      manifest: {
        name: 'skriptes',
        short_name: 'skriptes',
        description: 'Домашняя библиотека fb2-книг',
        lang: 'ru',
        // standalone — без браузерного chrome'а, выглядит как нативное
        // приложение. Альтернатива "minimal-ui" сохраняет URL-bar; нам
        // не нужен (host свой, конфиденциальности не учим).
        display: 'standalone',
        background_color: '#0a0a0a',
        theme_color: '#0a0a0a',
        // start_url=/ — открывается главная (BooksPage если залогинен,
        // иначе редирект на /login через TanStack Router).
        start_url: '/',
        scope: '/',
        icons: [
          // SVG-first: Chrome desktop, Edge, Firefox, Chrome Android 102+
          // выберут vector и масштабируют без потерь.
          {
            src: '/icon.svg',
            sizes: 'any',
            type: 'image/svg+xml',
            purpose: 'any maskable',
          },
          // PNG-fallback для старых движков и iOS Safari (apple-touch-icon
          // отдельный, через link tag — здесь дублируем для honest manifest).
          { src: '/icon-192.png', sizes: '192x192', type: 'image/png', purpose: 'any maskable' },
          { src: '/icon-512.png', sizes: '512x512', type: 'image/png', purpose: 'any maskable' },
        ],
      },
      workbox: {
        // Precache app-shell. JS-чанки уже идут с content-hash именами
        // (Vite default), так что навсегда-кэш безопасен.
        globPatterns: ['**/*.{js,css,html,svg,png,ico,woff2}'],
        // foliate-reader тяжёлый (~600 KiB JS), но нужен только когда
        // юзер реально открывает книгу в ридере. Прекэшировать сразу —
        // лишний траффик при первом заходе на сайт. Runtime-кэш ниже
        // подхватит при первом открытии и оставит навсегда. Шрифты ридера
        // (public/fonts, ~2,4 МБ на все) — так же: нужен один выбранный.
        globIgnores: ['**/foliate/**', '**/fonts/**/*.woff2'],
        // navigateFallback=index.html — все non-asset GET'ы (роуты SPA)
        // отдают index.html из кэша → offline-shell. Исключаем /api/* и
        // /opds/* — на эти пути ходить через сеть, кэш не должен их
        // перехватывать.
        //
        // /foliate-reader.html ОБЯЗАТЕЛЬНО в denylist: ридер открывает его в
        // iframe как /foliate-reader.html?src=...&cfi=... — это navigation-
        // запрос с query-параметрами. Precache-роут ключуется по голому
        // /foliate-reader.html и из-за query НЕ матчит → без denylist
        // navigateFallback отдаёт в iframe index.html (всё приложение!), и
        // ридер виснет на «Подготовка…». Denylist → запрос идёт в сеть (nginx
        // отдаёт настоящий foliate-reader.html).
        navigateFallback: '/index.html',
        navigateFallbackDenylist: [
          /^\/api\//,
          /^\/opds\//,
          /^\/healthz/,
          /^\/readyz/,
          /^\/foliate-reader\.html/,
        ],
        runtimeCaching: [
          // Обложки книг и фото авторов: content-addressable URLs
          // (/api/covers/{sha256.ext}), бесконечно immutable. CacheFirst
          // даёт мгновенную отдачу из кэша после первого скачивания.
          {
            urlPattern: /\/api\/covers\/[^/]+$/,
            handler: 'CacheFirst',
            options: {
              cacheName: 'skriptes-covers',
              expiration: {
                maxEntries: 500,
                maxAgeSeconds: 60 * 60 * 24 * 90, // 90 дней; LRU+TTL
              },
              cacheableResponse: { statuses: [0, 200] },
            },
          },
          // OPDS-cover endpoint — те же файлы, отдельная регулярка.
          {
            urlPattern: /\/opds\/covers\/[^/]+$/,
            handler: 'CacheFirst',
            options: {
              cacheName: 'skriptes-opds-covers',
              expiration: { maxEntries: 500, maxAgeSeconds: 60 * 60 * 24 * 90 },
              cacheableResponse: { statuses: [0, 200] },
            },
          },
          // foliate ridder (тяжёлый JS, статика) — кэшируется при
          // первом открытии ридера и далее offline-ready.
          // StaleWhileRevalidate, не CacheFirst: имена файлов без хэша, а мы
          // патчим вендоренный foliate (paginator.js) — CacheFirst держал бы
          // старую копию до 30 дней. Имя кэша с версией: смена (-v2 — патч
          // перелистывания) заставляет сразу взять свежие файлы.
          {
            urlPattern: /\/foliate\/.*$/,
            handler: 'StaleWhileRevalidate',
            options: {
              cacheName: 'skriptes-foliate-v4',
              expiration: { maxEntries: 20, maxAgeSeconds: 60 * 60 * 24 * 30 },
              cacheableResponse: { statuses: [0, 200] },
            },
          },
          // Шрифты ридера — CacheFirst: файлы не меняются; заменили шрифт —
          // бампнуть версию в имени кэша (имена файлов без хэша).
          {
            urlPattern: /\/fonts\/[^/]+\/[^/]+\.woff2$/,
            handler: 'CacheFirst',
            options: {
              cacheName: 'skriptes-fonts-v1',
              expiration: { maxEntries: 60, maxAgeSeconds: 60 * 60 * 24 * 365 },
              cacheableResponse: { statuses: [0, 200] },
            },
          },
          // epub для ридера — StaleWhileRevalidate: книга открывается сразу из
          // кэша (и оффлайн), свежая конвертация подтянется к следующему разу.
          // Свой кэш: файлы по мегабайту и больше, держим немного.
          {
            urlPattern: ({ url, request }) =>
              request.method === 'GET' && /^\/api\/books\/\d+\/epub$/.test(url.pathname),
            handler: 'StaleWhileRevalidate',
            options: {
              cacheName: 'skriptes-epub',
              expiration: { maxEntries: 20, maxAgeSeconds: 60 * 60 * 24 * 30 },
              cacheableResponse: { statuses: [0, 200] },
            },
          },
          // GET-данные приложения (книги/авторы/серии) — NetworkFirst: в сети
          // всегда свежие (в карточках есть прогресс чтения, отметки, оценки
          // пользователя), кэш — только оффлайн или если сеть не ответила за
          // 4 с. Не StaleWhileRevalidate: он отдаёт ответ ПРОШЛОГО запроса.
          //
          // Позиция чтения и заметки — мимо SW вовсе: устаревшая позиция
          // открывала книгу там, где её открывали в прошлый раз, и ридер
          // записывал это место поверх свежего (#609). Оффлайн ридер берёт
          // позицию из своей копии на устройстве (lib/readerPosition).
          // Скачивания — тоже мимо: это файлы, не данные.
          //
          // urlPattern сериализуется в sw.js — только аргументы, без внешних имён.
          {
            urlPattern: ({ url, request }) =>
              request.method === 'GET' &&
              (url.pathname.startsWith('/api/books') ||
                url.pathname.startsWith('/api/authors') ||
                url.pathname.startsWith('/api/series') ||
                url.pathname.startsWith('/api/search/suggest')) &&
              !/^\/api\/books\/\d+\/(position|annotations|download)$/.test(url.pathname),
            handler: 'NetworkFirst',
            options: {
              // -v2: в прежнем skriptes-data лежат устаревшие позиции — его
              // удаляет lib/pwa.ts (dropLegacyCaches).
              cacheName: 'skriptes-data-v2',
              networkTimeoutSeconds: 4,
              expiration: { maxEntries: 200, maxAgeSeconds: 60 * 60 * 24 * 7 }, // 7д TTL
              cacheableResponse: { statuses: [0, 200] },
            },
          },
          // /api/me/* и /api/auth/* НЕ перехватываем service worker'ом вовсе —
          // они идут напрямую в сеть (никаких runtime-правил под них нет, а
          // navigateFallback их не трогает: /api/* в denylist). Раньше тут было
          // NetworkOnly-правило, но оно лишь оборачивало сетевой сбой в невнятный
          // Workbox `no-response` в консоли. При недоступном бэкенде честнее
          // отдать нативную сетевую ошибку — её ловит apiFetch и показывает
          // «Сервер недоступен». User-specific данные и так не кэшируются.
        ],
      },
      // devOptions — SW в dev mode мешает горячему перезагру (Vite HMR);
      // включаем только если нужно дебажить SW локально (npm run dev → dev SW).
      devOptions: { enabled: false },
    }),
  ],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  preview: {
    headers: { 'Content-Security-Policy': readCSP() },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: process.env.VITE_API_URL ?? 'http://localhost:8080',
        changeOrigin: true,
      },
      '/opds': {
        target: process.env.VITE_API_URL ?? 'http://localhost:8080',
        changeOrigin: true,
      },
      '/healthz': process.env.VITE_API_URL ?? 'http://localhost:8080',
    },
  },
});
