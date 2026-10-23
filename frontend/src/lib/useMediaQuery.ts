import { useSyncExternalStore } from 'react';

/**
 * useMediaQuery — совпадает ли CSS media query сейчас (подписка на изменения).
 * Для решений, которые CSS не выразит (сколько чипов рендерить). Без
 * matchMedia (jsdom, SSR) — false.
 */
export function useMediaQuery(query: string): boolean {
  return useSyncExternalStore(
    (onChange) => {
      if (typeof window === 'undefined' || !window.matchMedia) return () => {};
      const mql = window.matchMedia(query);
      mql.addEventListener('change', onChange);
      return () => mql.removeEventListener('change', onChange);
    },
    () => (typeof window !== 'undefined' && window.matchMedia ? window.matchMedia(query).matches : false),
    () => false,
  );
}
