import { useEffect, useState } from 'react';

/**
 * useDebouncedValue — простой debounce React-стейта.
 * Между ребиндами возвращает предыдущее значение, после паузы delayMs —
 * подставляет новое. Нужен для поискового инпута: запрос к Meili
 * улетает не на каждое нажатие, а через delayMs тишины.
 */
export function useDebouncedValue<T>(value: T, delayMs = 200): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(t);
  }, [value, delayMs]);
  return debounced;
}
