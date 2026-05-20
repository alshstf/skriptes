import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import { VitePWA } from 'vite-plugin-pwa';
import path from 'node:path';

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    // ── PWA: service worker + web app manifest ──────────────────────
    // registerType=autoUpdate — новая версия SW активируется без
    // ручного "обновить страницу". Старые tab'ы доживают на старом SW,
    // следующая навигация подхватит новый.
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
        // подхватит при первом открытии и оставит навсегда.
        globIgnores: ['**/foliate/**'],
        // navigateFallback=index.html — все non-asset GET'ы (роуты SPA)
        // отдают index.html из кэша → offline-shell. Исключаем /api/* и
        // /opds/* — на эти пути ходить через сеть, кэш не должен их
        // перехватывать.
        navigateFallback: '/index.html',
        navigateFallbackDenylist: [/^\/api\//, /^\/opds\//, /^\/healthz/, /^\/readyz/],
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
          {
            urlPattern: /\/foliate\/.*$/,
            handler: 'CacheFirst',
            options: {
              cacheName: 'skriptes-foliate',
              expiration: { maxEntries: 20, maxAgeSeconds: 60 * 60 * 24 * 30 },
              cacheableResponse: { statuses: [0, 200] },
            },
          },
          // GET-данные приложения (книги/авторы/серии) — StaleWhileRevalidate:
          // моментально отдаём из кэша, в фоне обновляем. Если оффлайн —
          // показываем последнее что видели; новые карточки не доступны,
          // но "вернуться в недавнее" работает.
          {
            urlPattern: ({ url, request }) =>
              request.method === 'GET' &&
              (url.pathname.startsWith('/api/books') ||
                url.pathname.startsWith('/api/authors') ||
                url.pathname.startsWith('/api/series') ||
                url.pathname.startsWith('/api/search/suggest')),
            handler: 'StaleWhileRevalidate',
            options: {
              cacheName: 'skriptes-data',
              expiration: { maxEntries: 200, maxAgeSeconds: 60 * 60 * 24 * 7 }, // 7д TTL
              cacheableResponse: { statuses: [0, 200] },
            },
          },
          // /api/me/* и /api/auth/* — никогда не кэшируем. User-specific
          // данные (favorites, kindle targets, session) могут поменяться
          // между tab'ами, stale-кэш приведёт к рассинхрону.
          {
            urlPattern: ({ url }) =>
              url.pathname.startsWith('/api/me/') || url.pathname.startsWith('/api/auth/'),
            handler: 'NetworkOnly',
          },
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
