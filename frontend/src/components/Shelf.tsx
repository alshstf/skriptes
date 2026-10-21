import { useCallback, useEffect, useRef, useState } from 'react';
import { Link } from '@tanstack/react-router';
import { ChevronDown, ChevronLeft, ChevronRight, ChevronUp } from 'lucide-react';
import { BookCover } from '@/components/BookCover';
import { cn } from '@/lib/utils';
import { pluralBooks } from '@/lib/format';

/**
 * Shelf — горизонтальная КАРУСЕЛЬ карточек с заголовком (полки Главной).
 * Чтобы было очевидно, что контент скроллится (и на десктопе, и на мобиле):
 * градиент-затухание у краёв + круглые кнопки-стрелки. И то и другое
 * появляется только когда есть куда прокручивать (canLeft/canRight по факту
 * overflow).
 *
 * Сворачивание (план recsys/02, 2.9): с onToggle в заголовке появляется
 * шеврон; свёрнутая полка — одна строка «заголовок · N книг», карусель не
 * рендерится. Состояние хранит вызывающий (на Главной — user_settings).
 */
export function Shelf({
  title,
  children,
  collapsed = false,
  onToggle,
  count,
  controls,
}: {
  title: string;
  children: React.ReactNode;
  collapsed?: boolean;
  onToggle?: () => void;
  count?: number;
  // controls — контролы секции рядом с заголовком (чипы «N минут» у «На вечер»).
  controls?: React.ReactNode;
}) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const [canLeft, setCanLeft] = useState(false);
  const [canRight, setCanRight] = useState(false);

  const update = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    setCanLeft(el.scrollLeft > 1);
    setCanRight(el.scrollLeft + el.clientWidth < el.scrollWidth - 1);
  }, []);

  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    update();
    el.addEventListener('scroll', update, { passive: true });
    // ResizeObserver — пересчитать при смене ширины контейнера/догрузке карточек.
    const ro = new ResizeObserver(update);
    ro.observe(el);
    return () => {
      el.removeEventListener('scroll', update);
      ro.disconnect();
    };
  }, [update, collapsed]);

  function scrollByDir(dir: 1 | -1) {
    const el = scrollRef.current;
    if (!el) return;
    el.scrollBy({ left: dir * el.clientWidth * 0.8, behavior: 'smooth' });
  }

  const Chevron = collapsed ? ChevronDown : ChevronUp;
  return (
    <section className="space-y-3" data-collapsed={collapsed || undefined}>
      <div className="flex items-center gap-2">
        <h2 className="text-lg font-semibold tracking-tight">{title}</h2>
        {collapsed && count != null ? (
          <span className="text-xs text-muted-foreground">
            {count} {pluralBooks(count)}
          </span>
        ) : null}
        {!collapsed && controls ? <div className="flex items-center gap-1">{controls}</div> : null}
        {onToggle ? (
          <button
            type="button"
            onClick={onToggle}
            aria-expanded={!collapsed}
            aria-label={collapsed ? `Развернуть «${title}»` : `Свернуть «${title}»`}
            className="ml-auto flex size-7 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
          >
            <Chevron className="size-4" aria-hidden />
          </button>
        ) : null}
      </div>
      {collapsed ? null : (
        <div className="relative">
          {canLeft ? <ShelfArrow side="left" onClick={() => scrollByDir(-1)} /> : null}
          {canRight ? <ShelfArrow side="right" onClick={() => scrollByDir(1)} /> : null}
          <div ref={scrollRef} className="flex gap-4 overflow-x-auto scroll-smooth pb-2">
            {children}
          </div>
        </div>
      )}
    </section>
  );
}

// ShelfArrow — затухание у края полки + круглая кнопка-стрелка. Затухание
// (pointer-events-none) — пассивная подсказка «контент продолжается»; кнопка —
// активный контрол прокрутки (тапается и на мобиле, и на десктопе).
function ShelfArrow({ side, onClick }: { side: 'left' | 'right'; onClick: () => void }) {
  const Icon = side === 'left' ? ChevronLeft : ChevronRight;
  return (
    <>
      <div
        aria-hidden
        className={cn(
          'pointer-events-none absolute inset-y-0 z-10 w-10 from-background to-transparent',
          side === 'left' ? 'left-0 bg-gradient-to-r' : 'right-0 bg-gradient-to-l',
        )}
      />
      <button
        type="button"
        onClick={onClick}
        aria-label={side === 'left' ? 'Прокрутить назад' : 'Прокрутить вперёд'}
        className={cn(
          'absolute top-1/2 z-20 flex size-8 -translate-y-1/2 items-center justify-center rounded-full border border-border bg-background/90 text-muted-foreground shadow-sm backdrop-blur transition-colors hover:bg-accent hover:text-foreground',
          side === 'left' ? 'left-1' : 'right-1',
        )}
      >
        <Icon className="size-4" aria-hidden />
      </button>
    </>
  );
}

/** Ширина карточки полки — общая для карточек и скелетона. */
export const SHELF_CARD_WIDTH = 'w-28 sm:w-32';

/**
 * CoverCard — карточка-обложка фиксированной ширины для горизонтальной полки.
 * Обложка через общий BookCover (cover_path → /api/covers/{path}; иначе
 * on-demand /api/covers/book/{editionId}). Вся карточка — ссылка на карточку книги.
 */
export function CoverCard({
  to,
  title,
  authors,
  coverPath,
  coverEditionId,
  children,
}: {
  to: string;
  title: string;
  authors: string[];
  coverPath?: string;
  coverEditionId: number;
  children?: React.ReactNode;
}) {
  return (
    <Link
      to={to}
      className={cn(
        'group flex shrink-0 flex-col gap-1.5 rounded-md p-1 transition-colors hover:bg-accent/40',
        SHELF_CARD_WIDTH,
        'focus-visible:outline-2 focus-visible:outline-ring',
      )}
    >
      <BookCover
        coverPath={coverPath}
        src={`/api/covers/book/${coverEditionId}`}
        title={title}
        placeholder="monogram"
        className="w-full"
      />
      <p className="line-clamp-2 text-sm font-medium leading-snug">{title}</p>
      {authors.length > 0 ? (
        <p className="line-clamp-1 text-xs text-muted-foreground">{authors.join(', ')}</p>
      ) : null}
      {children}
    </Link>
  );
}

export function ShelfSkeleton({ title }: { title: string }) {
  return (
    <section className="space-y-3">
      <h2 className="text-lg font-semibold tracking-tight">{title}</h2>
      <div className="flex gap-4 overflow-hidden pb-2">
        {Array.from({ length: 6 }).map((_, i) => (
          <div key={i} className={cn('flex shrink-0 flex-col gap-1.5 p-1', SHELF_CARD_WIDTH)}>
            <div className="aspect-[2/3] w-full animate-pulse rounded-md bg-accent" />
            <div className="h-4 w-full animate-pulse rounded bg-accent" />
            <div className="h-3 w-2/3 animate-pulse rounded bg-accent" />
          </div>
        ))}
      </div>
    </section>
  );
}
