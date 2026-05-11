import '@testing-library/jest-dom/vitest';

// jsdom не реализует ResizeObserver — cmdk и Radix падают при mount.
// Простая no-op заглушка, достаточная для unit-тестов компонентов.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  (globalThis as unknown as { ResizeObserver: typeof ResizeObserverStub }).ResizeObserver =
    ResizeObserverStub;
}
