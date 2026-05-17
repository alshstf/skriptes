import js from '@eslint/js';
import globals from 'globals';
import reactHooks from 'eslint-plugin-react-hooks';
import reactRefresh from 'eslint-plugin-react-refresh';
import tseslint from 'typescript-eslint';

export default tseslint.config(
  {
    ignores: [
      'dist',
      'node_modules',
      'coverage',
      'playwright-report',
      'test-results',
      // foliate-js — vendor'нутая внешняя библиотека (frontend/public/foliate/).
      // Линтуется их собственным CI; для нас это статика, к нашим стайл-гайдам
      // не приводим.
      'public/foliate',
    ],
  },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ['**/*.{ts,tsx}'],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
    },
    plugins: {
      'react-hooks': reactHooks,
      'react-refresh': reactRefresh,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],
    },
  },
  {
    // shadcn-сгенерированные компоненты часто экспортируют и сам компонент,
    // и сопутствующие утилиты (cva variants и т.п.). Это канонический
    // паттерн — react-refresh для них отключаем.
    files: ['src/components/ui/**'],
    rules: {
      'react-refresh/only-export-components': 'off',
    },
  },
  {
    // Playwright fixtures (e2e/) используют функцию `use(value)` для
    // передачи fixture'а в тест — это convention библиотеки, а не
    // React-хук. ESLint react-hooks/rules-of-hooks ловит её по имени
    // (`use*` = hook) и валит lint. Хуков в e2e нет в принципе — отключаем.
    files: ['e2e/**'],
    rules: {
      'react-hooks/rules-of-hooks': 'off',
    },
  },
);
