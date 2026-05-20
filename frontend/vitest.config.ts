import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import path from 'node:path';

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
      // virtual:pwa-register создаётся плагином vite-plugin-pwa в
      // build/dev режиме. В vitest плагин не подключён (он мешал бы
      // тестам и не нужен в unit-сценариях), поэтому подменяем его на
      // пустую заглушку. registerPWA() в src/lib/pwa.ts ловит import
      // в try/catch и в случае пустого экспорта корректно no-op'ит.
      'virtual:pwa-register': path.resolve(__dirname, './src/test/pwa-register-stub.ts'),
    },
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    css: true,
    // По умолчанию vitest подхватывает все *.spec.ts/*.test.ts по проекту.
    // e2e/ — для Playwright (см. playwright.config.ts) и использует
    // другой test() runner; vitest запускает их и падает с
    // "Playwright Test did not expect test() to be called here".
    exclude: ['**/node_modules/**', '**/dist/**', 'e2e/**', '**/playwright-report/**'],
  },
});
