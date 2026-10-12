import { useEffect, useRef, useState } from 'react';

/** Ширина элемента (ResizeObserver) — для SVG-полосок и раскладок, которые
 *  зависят от контейнера, а не от вьюпорта. */
export function useContainerWidth<T extends HTMLElement>() {
  const ref = useRef<T>(null);
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof ResizeObserver === 'undefined') return;
    const obs = new ResizeObserver(() => setWidth(el.clientWidth));
    obs.observe(el);
    setWidth(el.clientWidth);
    return () => obs.disconnect();
  }, []);
  return [ref, width] as const;
}
