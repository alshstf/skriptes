import '@testing-library/jest-dom/vitest';
import { afterEach } from 'vitest';

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

// jsdom в нашей версии не выдаёт localStorage (sessionStorage есть,
// а localStorage — нет; известный гэп Node 26 + jsdom 25). Минимальный
// Storage-совместимый shim на основе Map; глобально + на window для
// кода который читает window.localStorage явно.
if (typeof window !== 'undefined' && !window.localStorage) {
  const store = new Map<string, string>();
  const localStorageStub: Storage = {
    get length() {
      return store.size;
    },
    clear() {
      store.clear();
    },
    getItem(key: string) {
      return store.get(key) ?? null;
    },
    key(index: number) {
      return Array.from(store.keys())[index] ?? null;
    },
    removeItem(key: string) {
      store.delete(key);
    },
    setItem(key: string, value: string) {
      store.set(key, String(value));
    },
  };
  Object.defineProperty(window, 'localStorage', {
    value: localStorageStub,
    configurable: true,
  });
  Object.defineProperty(globalThis, 'localStorage', {
    value: localStorageStub,
    configurable: true,
  });
  // Чистим между тестами — иначе сохранённые на одном тесте ключи
  // утекут в следующий и сделают порядок исполнения значимым.
  afterEach(() => {
    store.clear();
  });
}
