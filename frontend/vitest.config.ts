import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import path from 'node:path';

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
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
