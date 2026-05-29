import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Badge, badgeVariants } from '@/components/ui/badge';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { useGenreMap } from '@/lib/genres';
import { useGenreChipStyle, genreChipClass } from '@/lib/appearance';
import { cn } from '@/lib/utils';

/**
 * GenreChips — жанровые плашки книги в ОДНУ строку.
 *
 * Влезает столько чипсов, сколько помещается; остальные прячутся за
 * кликабельным «+N», который сам тоже учитывается в ширине строки. Клик
 * по «+N» открывает поповер с невлезшими жанрами и НЕ навигирует —
 * компонент живёт внутри кликабельной карточки-ссылки (stretched-link),
 * поэтому «+N» поднят над её ::after через relative z-10, а навигация
 * глушится preventDefault/stopPropagation.
 *
 * Сколько влезает — считаем измерением. Чтобы НЕ дублировать чипсы в DOM
 * (это ломало бы text-запросы в тестах и вредно для a11y), используем
 * две фазы:
 *   1. measuring — рендерим ВСЕ чипсы + образец «+N» видимо, в
 *      useLayoutEffect (до paint) снимаем их offsetWidth в кэш;
 *   2. done — рендерим только влезшие + «+N». Пересчёт ширины на
 *      ResizeObserver идёт из кэша, без повторного замера.
 * Переход measuring→done происходит до отрисовки → без мигания.
 *
 * jsdom не считает layout (offsetWidth=0) → там влезают все чипсы без
 * «+N»; реальное overflow-поведение проверяется Playwright'ом.
 */

const GAP_PX = 4; // соответствует gap-1 (0.25rem)

export function GenreChips({ genres, highlight }: { genres: string[]; highlight?: string[] }) {
  const genreMap = useGenreMap();
  const label = (code: string) => genreMap.get(code)?.display ?? code;
  const chipCls = genreChipClass(useGenreChipStyle());

  // Если активен фильтр по жанрам — совпавшие двигаем в начало (стабильно),
  // чтобы именно они попадали в видимые позиции, а не прятались за «+N».
  // Иначе странно: ищешь «Научная фантастика», а у книги видны «Детектив,
  // Хоррор», и совпавший жанр — под «+N».
  const ordered = (() => {
    if (!highlight || highlight.length === 0) return genres;
    const hi = new Set(highlight);
    const matched = genres.filter((g) => hi.has(g));
    if (matched.length === 0) return genres;
    const rest = genres.filter((g) => !hi.has(g));
    return [...matched, ...rest];
  })();

  const rowRef = useRef<HTMLDivElement>(null);
  const widthsRef = useRef<number[]>([]);
  const plusWRef = useRef(0);

  const [measuring, setMeasuring] = useState(true);
  const [visible, setVisible] = useState(ordered.length);

  // Ключ зависит от отображаемых имён: меняется и при смене набора
  // жанров, и когда подгрузился словарь (коды → display) — тогда нужно
  // переснять ширины.
  const labelsKey = ordered.map(label).join('');

  // Сброс в фазу замера при изменении содержимого/ширин.
  useLayoutEffect(() => {
    setMeasuring(true);
    setVisible(ordered.length);
  }, [labelsKey, ordered.length]);

  const computeVisible = (avail: number): number => {
    const w = widthsRef.current;
    // Ширина строки ещё не известна (layout не посчитан — например jsdom,
    // или скрытый display:none-родитель). Показываем все; реальный
    // пересчёт придёт на ResizeObserver, когда появится ширина.
    if (avail <= 0) return ordered.length;
    if (w.length === 0) return ordered.length;
    let sumAll = 0;
    w.forEach((x, i) => {
      sumAll += x + (i > 0 ? GAP_PX : 0);
    });
    if (sumAll <= avail) return w.length; // всё влезает — без «+N»
    const plusW = plusWRef.current;
    let used = 0;
    let count = 0;
    for (let i = 0; i < w.length; i++) {
      const add = (count > 0 ? GAP_PX : 0) + w[i];
      if (used + add + GAP_PX + plusW <= avail) {
        used += add;
        count++;
      } else {
        break;
      }
    }
    return count;
  };

  // Фаза замера: снять ширины и решить, сколько влезает.
  useLayoutEffect(() => {
    if (!measuring) return;
    const row = rowRef.current;
    if (!row) return;
    const chips = Array.from(row.querySelectorAll<HTMLElement>('[data-chip]'));
    widthsRef.current = chips.map((el) => el.offsetWidth);
    const plusEl = row.querySelector<HTMLElement>('[data-plus]');
    plusWRef.current = plusEl ? plusEl.offsetWidth : 0;
    setVisible(computeVisible(row.clientWidth));
    setMeasuring(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [measuring, labelsKey]);

  // Пересчёт при изменении ширины строки (из кэша ширин).
  useEffect(() => {
    const row = rowRef.current;
    if (!row) return;
    const ro = new ResizeObserver(() => {
      if (!measuring) setVisible(computeVisible(row.clientWidth));
    });
    ro.observe(row);
    return () => ro.disconnect();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [measuring]);

  if (ordered.length === 0) return null;

  // Фаза замера: все чипсы + образец «+N» в DOM один раз (видимы, но
  // до paint заменятся срезом — мигания нет).
  if (measuring) {
    return (
      <div
        ref={rowRef}
        data-testid="genre-chips"
        className="relative flex items-center gap-1 overflow-hidden pt-1"
      >
        {ordered.map((g) => (
          <Badge key={g} data-chip="" variant="secondary" className={chipCls}>
            {label(g)}
          </Badge>
        ))}
        <span data-plus="" className={cn(badgeVariants({ variant: 'secondary' }), chipCls)}>
          +{ordered.length}
        </span>
      </div>
    );
  }

  const shown = ordered.slice(0, visible);
  const hidden = ordered.slice(visible);

  return (
    <div
      ref={rowRef}
      data-testid="genre-chips"
      className="relative flex items-center gap-1 overflow-hidden pt-1"
    >
      {shown.map((g) => (
        <Badge key={g} variant="secondary" className={chipCls}>
          {label(g)}
        </Badge>
      ))}

      {hidden.length > 0 ? (
        <Popover>
          <PopoverTrigger asChild>
            <button
              type="button"
              // НЕ preventDefault: Radix composeEventHandlers пропустит своё
              // открытие поповера, если default предотвращён. Навигацию
              // глушит z-10 (кнопка перехватывает клик поверх stretched-
              // link'а ::after); stopPropagation — на всякий случай.
              onClick={(e) => e.stopPropagation()}
              className={cn(
                badgeVariants({ variant: 'secondary' }),
                chipCls,
                'relative z-10 cursor-pointer hover:bg-muted hover:text-foreground',
              )}
              aria-label={`Ещё ${hidden.length} жанр(ов)`}
            >
              +{hidden.length}
            </button>
          </PopoverTrigger>
          <PopoverContent align="start" className="w-auto max-w-xs" onClick={(e) => e.stopPropagation()}>
            <div className="flex flex-wrap gap-1">
              {hidden.map((g) => (
                <Badge key={g} variant="secondary" className={chipCls}>
                  {label(g)}
                </Badge>
              ))}
            </div>
          </PopoverContent>
        </Popover>
      ) : null}
    </div>
  );
}
