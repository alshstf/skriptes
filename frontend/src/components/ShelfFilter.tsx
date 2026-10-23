import { useEffect, useRef, useState } from 'react';
import { Search, X } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { cn } from '@/lib/utils';

/**
 * ShelfFilter — поле «Фильтр по названию» под шапкой раскрытой полки (#582).
 *
 * sticky: длинная полка (избранное на 50+, подборка экранизаций) — поле
 * закреплено сразу под шапкой приложения, пока полка в видимой области
 * (sticky ограничен своим <li>). top — как у саб-бара /books: высота шапки
 * (h-14) + safe-area-инсет (грабля 18). Тень — только когда поле прилипло.
 */
export function ShelfFilter({
  value,
  onChange,
  sticky = false,
  status,
}: {
  value: string;
  onChange: (v: string) => void;
  sticky?: boolean;
  /** Строка под полем при активном фильтре: «Найдено 3 из 42». */
  status?: string;
}) {
  const sentinel = useRef<HTMLDivElement>(null);
  const stuck = useStuck(sentinel, sticky);
  return (
    <>
      {sticky ? <div ref={sentinel} aria-hidden /> : null}
      <div
        data-testid="shelf-filter"
        className={cn(
          'border-b border-border/60 bg-background px-3 py-2',
          sticky && 'sticky top-[calc(env(safe-area-inset-top)+3.5rem)] z-10',
          sticky && stuck && 'shadow-[0_10px_22px_-10px_rgba(0,0,0,0.9)]',
        )}
      >
        <div className="relative">
          <Search
            className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground"
            aria-hidden
          />
          <Input
            type="search"
            value={value}
            onChange={(e) => onChange(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Escape' && value) {
                e.preventDefault();
                onChange('');
              }
            }}
            placeholder="Фильтр по названию или автору"
            aria-label="Фильтр по названию"
            className="pr-8 pl-8 [&::-webkit-search-cancel-button]:hidden"
          />
          {value ? (
            <button
              type="button"
              onClick={() => onChange('')}
              aria-label="Очистить фильтр"
              className="absolute top-1/2 right-1 flex size-7 -translate-y-1/2 items-center justify-center rounded text-muted-foreground transition hover:text-foreground"
            >
              <X className="size-4" aria-hidden />
            </button>
          ) : null}
        </div>
        {value && status ? (
          <p className="mt-1 text-xs text-muted-foreground" aria-live="polite">
            {status}
          </p>
        ) : null}
      </div>
    </>
  );
}

/**
 * useStuck — прилипло ли sticky-поле: нулевой «маячок» прямо перед ним уехал
 * выше линии прилипания (низа шапки). Пересчёт по скроллу через rAF.
 */
function useStuck(sentinel: React.RefObject<HTMLDivElement | null>, enabled: boolean): boolean {
  const [stuck, setStuck] = useState(false);
  useEffect(() => {
    const el = sentinel.current;
    if (!enabled || !el) return;
    const line = el.nextElementSibling;
    let raf = 0;
    const check = () => {
      raf = 0;
      const top = line ? parseFloat(getComputedStyle(line).top) || 0 : 0;
      setStuck(el.getBoundingClientRect().top < top - 0.5);
    };
    const onScroll = () => {
      if (!raf) raf = requestAnimationFrame(check);
    };
    check();
    window.addEventListener('scroll', onScroll, { passive: true });
    window.addEventListener('resize', onScroll);
    return () => {
      window.removeEventListener('scroll', onScroll);
      window.removeEventListener('resize', onScroll);
      if (raf) cancelAnimationFrame(raf);
    };
  }, [sentinel, enabled]);
  return enabled && stuck;
}

