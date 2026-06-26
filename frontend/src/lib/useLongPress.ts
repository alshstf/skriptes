import { useRef, type MouseEvent } from 'react';

/**
 * useLongPress — лонг-тап (тач) для вызова действия (правка оверрайда на мобиле,
 * грабля №19). preventDefault на contextmenu подавляет нативное long-press-меню
 * браузера. Возвращает набор тач-хендлеров для спреда на элемент.
 */
export function useLongPress(onLongPress: () => void, ms = 450) {
  const timer = useRef<number | null>(null);
  const clear = () => {
    if (timer.current) {
      clearTimeout(timer.current);
      timer.current = null;
    }
  };
  return {
    onTouchStart: () => {
      clear();
      timer.current = window.setTimeout(onLongPress, ms);
    },
    onTouchEnd: clear,
    onTouchMove: clear,
    onTouchCancel: clear,
    onContextMenu: (e: MouseEvent) => e.preventDefault(),
  };
}
