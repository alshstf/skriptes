import { useId } from 'react';
import { X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { GroupedGenresFilter } from '@/components/GroupedGenresFilter';
import { useGenreMap } from '@/lib/genres';
import { useEffectiveContent, useLanguageMap, useSrcLanguageMap } from '@/lib/content';
import { cn } from '@/lib/utils';
import type { FacetDistribution } from '@/lib/books';

/**
 * FiltersSidebar — панель фильтров для /books.
 *
 * Контролируемый компонент: текущее состояние приходит сверху через
 * value, изменения уходят через onChange (единый колбэк с новым value).
 * Это позволяет BooksPage хранить состояние в URL search-params и
 * никаких внутренних useState здесь не нужно.
 *
 * Facets (распределения с count) опциональны — если их нет, рисуем
 * без счётчиков. Без значений жанры/языки берутся из тех, что уже
 * выбраны пользователем (чтобы можно было отжать выбранный фильтр,
 * даже если в результатах его уже нет).
 */
export type FiltersValue = {
  genres: string[];
  lang: string;
  /** Язык ОРИГИНАЛА (fb2 src-lang) — независимый от языка издания фильтр. */
  srcLang: string;
  yearFrom: number;
  yearTo: number;
  sort: '' | 'year_desc' | 'year_asc' | 'popularity';
};

const SORT_OPTIONS: { value: FiltersValue['sort']; label: string }[] = [
  { value: '', label: 'По релевантности' },
  { value: 'year_desc', label: 'Сначала новые' },
  { value: 'year_asc', label: 'Сначала старые' },
  { value: 'popularity', label: 'По популярности' },
];

export function FiltersSidebar({
  value,
  onChange,
  facets,
  totalActive,
  onReset,
}: {
  value: FiltersValue;
  onChange: (next: FiltersValue) => void;
  facets?: FacetDistribution;
  totalActive: number;
  onReset: () => void;
}) {
  // Скрытые из выдачи жанры/языки (admin ∪ персональные) — не показываем
  // их в панели фильтров. Бэкенд уже исключает их из выдачи; это для
  // чистого UI. Языки исключены из фасетов на бэке, поэтому FacetRadios
  // и так их не покажет — отдельно фильтровать не нужно.
  const effective = useEffectiveContent();
  const hiddenGenres = effective.data?.hidden_genres;
  // Резолвим код языка фасета (ru) в имя (Русский). Фолбэк — сам код.
  const langMap = useLanguageMap();
  // Языки оригинала — отдельная карта: src-язык может отсутствовать среди
  // языков ИЗДАНИЙ коллекции (langMap его не знает).
  const srcLangMap = useSrcLanguageMap();
  return (
    <aside className="space-y-6 text-sm" aria-label="Фильтры">
      <div className="flex items-center justify-between">
        <h2 className="font-semibold">Фильтры</h2>
        {totalActive > 0 ? (
          <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={onReset}>
            Сбросить
          </Button>
        ) : null}
      </div>

      <SortBlock
        value={value.sort}
        onChange={(sort) => onChange({ ...value, sort })}
      />

      <YearBlock
        from={value.yearFrom}
        to={value.yearTo}
        onChange={(yearFrom, yearTo) => onChange({ ...value, yearFrom, yearTo })}
      />

      <GroupedGenresFilter
        selected={value.genres}
        facets={facets?.genres}
        onChange={(genres) => onChange({ ...value, genres })}
        hiddenCodes={hiddenGenres}
      />

      <FacetRadios
        title="Язык"
        selected={value.lang}
        facetKey="lang"
        facets={facets}
        onChange={(lang) => onChange({ ...value, lang })}
        labelFor={(code) => langMap.get(code) ?? code}
      />

      <FacetRadios
        title="Язык оригинала"
        selected={value.srcLang}
        facetKey="src_lang"
        facets={facets}
        onChange={(srcLang) => onChange({ ...value, srcLang })}
        labelFor={(code) => srcLangMap.get(code) ?? langMap.get(code) ?? code}
      />
    </aside>
  );
}

function SortBlock({
  value,
  onChange,
}: {
  value: FiltersValue['sort'];
  onChange: (next: FiltersValue['sort']) => void;
}) {
  const id = useId();
  return (
    <div className="space-y-2">
      <label htmlFor={id} className="block text-xs font-medium text-muted-foreground uppercase">
        Сортировка
      </label>
      <select
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value as FiltersValue['sort'])}
        className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm shadow-xs focus-visible:ring-2 focus-visible:ring-ring"
      >
        {SORT_OPTIONS.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </div>
  );
}

function YearBlock({
  from,
  to,
  onChange,
}: {
  from: number;
  to: number;
  onChange: (from: number, to: number) => void;
}) {
  return (
    <div className="space-y-2">
      <div className="text-xs font-medium text-muted-foreground uppercase">Год</div>
      <div className="flex items-center gap-2">
        <Input
          type="number"
          inputMode="numeric"
          placeholder="от"
          aria-label="Год от"
          value={from || ''}
          min={0}
          max={3000}
          onChange={(e) => onChange(parseYear(e.target.value), to)}
          className="h-9"
        />
        <span className="text-muted-foreground">—</span>
        <Input
          type="number"
          inputMode="numeric"
          placeholder="до"
          aria-label="Год до"
          value={to || ''}
          min={0}
          max={3000}
          onChange={(e) => onChange(from, parseYear(e.target.value))}
          className="h-9"
        />
      </div>
    </div>
  );
}

/** Радио-кнопки по facet'у (для single-value language). */
function FacetRadios({
  title,
  selected,
  facetKey,
  facets,
  onChange,
  labelFor,
}: {
  title: string;
  selected: string;
  facetKey: string;
  facets?: FacetDistribution;
  onChange: (next: string) => void;
  // Маппинг значения фасета в отображаемую подпись (напр. код языка → имя).
  // value остаётся ключом/значением для onChange; меняется только текст.
  labelFor?: (value: string) => string;
}) {
  const items = mergeFacetItems(facets?.[facetKey], selected ? [selected] : []);
  if (items.length === 0) return null;
  return (
    <div className="space-y-2">
      <div className="text-xs font-medium text-muted-foreground uppercase">{title}</div>
      <ul className="space-y-1">
        <li>
          <button
            type="button"
            onClick={() => onChange('')}
            className={cn(
              'flex w-full items-center gap-2 rounded px-1 py-0.5 text-left text-sm hover:bg-accent/40',
              selected === '' ? 'font-semibold' : '',
            )}
          >
            <span className="flex-1">Любой</span>
          </button>
        </li>
        {items.map(({ value, count }) => (
          <li key={value}>
            <button
              type="button"
              onClick={() => onChange(value)}
              className={cn(
                'flex w-full items-center gap-2 rounded px-1 py-0.5 text-left text-sm hover:bg-accent/40',
                selected === value ? 'font-semibold' : '',
              )}
            >
              <span className="flex-1 truncate">{labelFor ? labelFor(value) : value}</span>
              {count != null ? (
                <span className="text-xs tabular-nums text-muted-foreground">{count}</span>
              ) : null}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

function mergeFacetItems(
  dist: Record<string, number> | undefined,
  selected: string[],
): { value: string; count: number | null }[] {
  const map = new Map<string, number | null>();
  if (dist) {
    for (const [k, v] of Object.entries(dist)) {
      map.set(k, v);
    }
  }
  for (const s of selected) {
    if (!map.has(s)) map.set(s, null);
  }
  return Array.from(map.entries())
    .map(([value, count]) => ({ value, count }))
    .sort((a, b) => {
      // Сначала с count, потом по алфавиту.
      const ac = a.count ?? -1;
      const bc = b.count ?? -1;
      if (ac !== bc) return bc - ac;
      return a.value.localeCompare(b.value);
    });
}

function parseYear(raw: string): number {
  const n = Number(raw);
  if (!Number.isFinite(n)) return 0;
  if (n < 0 || n > 3000) return 0;
  return Math.floor(n);
}

/**
 * ActiveFilterChips — горизонтальная полоска чипов с активными фильтрами
 * и крестиками для удаления. Рендерится над списком, не в сайдбаре, но
 * живёт в той же файле для удобства.
 */
export function ActiveFilterChips({
  value,
  onChange,
}: {
  value: FiltersValue & { seriesId?: number; authorId?: number; query?: string };
  onChange: (next: FiltersValue & { seriesId?: number; authorId?: number; query?: string }) => void;
}) {
  // Переводим fb2_code в человеческое display-имя если справочник
  // жанров уже подгружен. Иначе показываем сырой код (fallback
  // когда useGenres ещё в полёте; редкий случай).
  const genreMap = useGenreMap();
  const langMap = useLanguageMap();
  const srcLangMap = useSrcLanguageMap();
  const chips: { label: string; onRemove: () => void }[] = [];
  for (const g of value.genres) {
    const display = genreMap.get(g)?.display ?? g;
    chips.push({
      label: `Жанр: ${display}`,
      onRemove: () =>
        onChange({ ...value, genres: value.genres.filter((x) => x !== g) }),
    });
  }
  if (value.lang) {
    chips.push({
      label: `Язык: ${langMap.get(value.lang) ?? value.lang}`,
      onRemove: () => onChange({ ...value, lang: '' }),
    });
  }
  if (value.srcLang) {
    chips.push({
      label: `Оригинал: ${srcLangMap.get(value.srcLang) ?? langMap.get(value.srcLang) ?? value.srcLang}`,
      onRemove: () => onChange({ ...value, srcLang: '' }),
    });
  }
  if (value.yearFrom || value.yearTo) {
    const label =
      value.yearFrom && value.yearTo
        ? `Год: ${value.yearFrom}–${value.yearTo}`
        : value.yearFrom
          ? `Год: от ${value.yearFrom}`
          : `Год: до ${value.yearTo}`;
    chips.push({
      label,
      onRemove: () => onChange({ ...value, yearFrom: 0, yearTo: 0 }),
    });
  }
  if (value.seriesId) {
    chips.push({
      label: 'Серия выбрана',
      onRemove: () => onChange({ ...value, seriesId: undefined }),
    });
  }
  if (value.authorId) {
    chips.push({
      label: 'Автор выбран',
      onRemove: () => onChange({ ...value, authorId: undefined }),
    });
  }
  if (chips.length === 0) return null;
  return (
    <div className="flex flex-wrap items-center gap-2">
      {chips.map((c) => (
        <Badge
          key={c.label}
          variant="secondary"
          className="gap-1 pl-2 pr-1 text-xs font-normal"
        >
          <span>{c.label}</span>
          <button
            type="button"
            onClick={c.onRemove}
            aria-label={`Снять фильтр: ${c.label}`}
            className="inline-flex size-4 items-center justify-center rounded hover:bg-background/60"
          >
            <X className="size-3" />
          </button>
        </Badge>
      ))}
    </div>
  );
}
