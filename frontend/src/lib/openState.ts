import { useCallback } from 'react';
import { useNavigate, useSearch } from '@tanstack/react-router';

// Раскрытые полки и подборки /shelves — в адресе (?open=…, #469): «Назад» с
// карточки книги возвращает их раскрытыми, а восстановленная прокрутка
// роутера (scrollRestoration) встаёт на ту же книгу. Ключ — «вид:id»
// («p:adaptations», «s:12», «c:5»).

/** useOpenToggle — раскрыт ли блок key и переключатель (без смены прокрутки). */
export function useOpenToggle(key: string): [boolean, () => void] {
  const { open } = useSearch({ strict: false }) as { open?: string[] };
  const navigate = useNavigate();
  const isOpen = open?.includes(key) ?? false;
  const toggle = useCallback(() => {
    void navigate({
      to: '.',
      search: (prev: { open?: string[] }) => {
        const cur = prev.open ?? [];
        const next = cur.includes(key) ? cur.filter((k) => k !== key) : [...cur, key];
        return { ...prev, open: next.length > 0 ? next : undefined };
      },
      replace: true,
      resetScroll: false,
    });
  }, [key, navigate]);
  return [isOpen, toggle];
}
