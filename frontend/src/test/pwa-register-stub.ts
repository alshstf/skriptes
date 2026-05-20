// Заглушка для virtual:pwa-register в vitest. См. vitest.config.ts.
// Реальный модуль создаётся vite-plugin-pwa плагином, тут — no-op.
//
// registerSW возвращает «функцию-detach» как и настоящий модуль; никакие
// callback'и не вызываются (нет SW в jsdom).
export function registerSW() {
  return async () => {};
}
