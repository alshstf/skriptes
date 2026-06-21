import { useMemo, useState } from 'react';
import { Link } from '@tanstack/react-router';
import { Bell, BookHeart, Film, Landmark, Search, SlidersHorizontal, User as UserIcon } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Callout } from '@/components/ui/callout';
import { Switch } from '@/components/ui/switch';
import {
  Sheet,
  SheetContent,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from '@/components/ui/sheet';
import { GroupedGenresFilter } from '@/components/GroupedGenresFilter';
import { useDebouncedValue } from '@/lib/useDebouncedValue';
import { useGenreMap } from '@/lib/genres';
import { useEffectiveContent, useLanguageMap, useLanguages } from '@/lib/content';
import { useGenreChipStyle, genreChipClass } from '@/lib/appearance';
import { useAuthorsList, type AuthorListItem, type AuthorsListParams } from '@/lib/authors';
import { cn } from '@/lib/utils';

const PAGE_SIZE = 50;
// MAX_LIMIT — потолок limit'а на бэке (catalog.sanitizePaging, ≤500). Пагинация
// тут — рост limit'а (а не offset), поэтому за этим потолком сервер молча
// перестаёт отдавать больше; клампим, чтобы «Показать ещё» не висел вечно.
const MAX_LIMIT = 500;

// AuthorsFilters — состояние панели фильтров (локальное, без URL: раздел
// самодостаточен, пагинация — кнопкой «Показать ещё»).
type AuthorsFilters = {
  genres: string[];
  langs: string[];
  yearFrom: number;
  yearTo: number;
  hasAdaptations: boolean;
  minRating: number;
  minReaderRating: number;
  favoritesOnly: boolean;
  sort: NonNullable<AuthorsListParams['sort']>;
};

const EMPTY_FILTERS: AuthorsFilters = {
  genres: [],
  langs: [],
  yearFrom: 0,
  yearTo: 0,
  hasAdaptations: false,
  minRating: 0,
  minReaderRating: 0,
  favoritesOnly: false,
  sort: 'name',
};

export function AuthorsPage() {
  const [queryInput, setQueryInput] = useState('');
  const debouncedQuery = useDebouncedValue(queryInput, 200);
  const [filters, setFilters] = useState<AuthorsFilters>(EMPTY_FILTERS);
  const [limit, setLimit] = useState(PAGE_SIZE);
  const [filtersOpen, setFiltersOpen] = useState(false);

  // Любое изменение фильтра/поиска сбрасывает пагинацию: меняем filters →
  // сбрасываем limit к первой странице через производный ключ ниже.
  const applyFilters = (next: AuthorsFilters) => {
    setFilters(next);
    setLimit(PAGE_SIZE);
  };

  const { data, isLoading, isFetching, error } = useAuthorsList({
    query: debouncedQuery,
    genres: filters.genres,
    langs: filters.langs,
    yearFrom: filters.yearFrom,
    yearTo: filters.yearTo,
    hasAdaptations: filters.hasAdaptations,
    minRating: filters.minRating,
    minReaderRating: filters.minReaderRating,
    favoritesOnly: filters.favoritesOnly,
    sort: filters.sort,
    limit,
  });

  const items = data?.items ?? [];
  const total = data?.total ?? 0;
  // Ещё есть что грузить, если показано меньше общего числа И мы не упёрлись в
  // потолок limit'а (за ним сервер обрежет, а total остался бы больше).
  const hasMore = items.length < total && limit < MAX_LIMIT;

  const totalActive =
    filters.genres.length +
    filters.langs.length +
    (filters.yearFrom || filters.yearTo ? 1 : 0) +
    (filters.hasAdaptations ? 1 : 0) +
    (filters.minRating ? 1 : 0) +
    (filters.minReaderRating ? 1 : 0) +
    (filters.favoritesOnly ? 1 : 0) +
    (filters.sort !== 'name' ? 1 : 0);

  const resetAll = () => {
    setQueryInput('');
    applyFilters(EMPTY_FILTERS);
  };

  return (
    <div className="grid grid-cols-1 gap-6 md:grid-cols-[260px_minmax(0,1fr)]">
      <div className="hidden md:block">
        <AuthorsFiltersSidebar value={filters} onChange={applyFilters} totalActive={totalActive} onReset={resetAll} />
      </div>

      <div className="space-y-4">
        <div className="sticky top-14 z-10 -mx-4 bg-background px-4 py-2 md:static md:top-auto md:z-auto md:mx-0 md:bg-transparent md:px-0 md:py-0">
          <div className="flex items-center gap-2">
            <div className="relative flex-1 md:max-w-md">
              <Search
                className="absolute left-3 top-1/2 -translate-y-1/2 size-4 text-muted-foreground"
                aria-hidden
              />
              <Input
                type="search"
                placeholder="Поиск по имени автора"
                className="pl-9"
                value={queryInput}
                onChange={(e) => {
                  setQueryInput(e.target.value);
                  setLimit(PAGE_SIZE);
                }}
              />
            </div>

            {/* Мобильная кнопка фильтров — только < md. */}
            <Sheet open={filtersOpen} onOpenChange={setFiltersOpen}>
              <SheetTrigger asChild>
                <Button
                  variant="outline"
                  size="icon"
                  aria-label="Фильтры"
                  className="relative shrink-0 md:hidden"
                >
                  <SlidersHorizontal className="size-4" aria-hidden />
                  {totalActive > 0 ? (
                    <Badge
                      className="absolute -right-1.5 -top-1.5 size-4 justify-center rounded-full p-0 text-[10px] tabular-nums"
                      aria-hidden
                    >
                      {totalActive}
                    </Badge>
                  ) : null}
                </Button>
              </SheetTrigger>
              <SheetContent side="left" className="w-[85%] gap-0 p-0" onOpenAutoFocus={(e) => e.preventDefault()}>
                <SheetHeader className="sr-only">
                  <SheetTitle>Фильтры</SheetTitle>
                </SheetHeader>
                <div className="flex-1 overflow-y-auto p-4 pt-12">
                  <AuthorsFiltersSidebar
                    value={filters}
                    onChange={applyFilters}
                    totalActive={totalActive}
                    onReset={resetAll}
                  />
                </div>
                <SheetFooter className="border-t">
                  <Button onClick={() => setFiltersOpen(false)}>
                    {data ? `Показать ${total} ${pluralAuthors(total)}` : 'Показать'}
                  </Button>
                </SheetFooter>
              </SheetContent>
            </Sheet>
          </div>
        </div>

        {data ? (
          <p className="text-sm text-muted-foreground tabular-nums">
            {total} {pluralAuthors(total)}
          </p>
        ) : null}

        {error ? (
          <p role="alert" className="text-sm text-destructive">
            Не удалось загрузить список: {(error as Error).message}
          </p>
        ) : null}

        {isLoading ? (
          <AuthorsSkeleton />
        ) : data && items.length === 0 ? (
          <Callout>Авторов по заданным фильтрам не нашлось.</Callout>
        ) : data ? (
          <>
            <ul className={cn('space-y-1', isFetching ? 'opacity-70' : '')}>
              {items.map((a) => (
                <li key={a.id}>
                  <AuthorRow author={a} />
                </li>
              ))}
            </ul>
            {hasMore ? (
              <div className="pt-2">
                <Button
                  variant="outline"
                  className="w-full"
                  onClick={() => setLimit((n) => Math.min(n + PAGE_SIZE, MAX_LIMIT))}
                  disabled={isFetching}
                >
                  {isFetching ? 'Загрузка…' : 'Показать ещё'}
                </Button>
              </div>
            ) : items.length > 0 ? (
              <p className="pt-2 text-center text-xs text-muted-foreground">Это все авторы</p>
            ) : null}
          </>
        ) : null}
      </div>
    </div>
  );
}

// ── строка автора ───────────────────────────────────────────────────

function AuthorRow({ author }: { author: AuthorListItem }) {
  const genreMap = useGenreMap();
  const langMap = useLanguageMap();
  const chipCls = genreChipClass(useGenreChipStyle());

  const years = author.years_active
    ? author.years_active.from === author.years_active.to
      ? String(author.years_active.from)
      : `${author.years_active.from}–${author.years_active.to}`
    : null;

  const langNames = (author.languages ?? []).slice(0, 4).map((c) => langMap.get(c) ?? c);

  return (
    <Link
      to="/authors/$id"
      params={{ id: String(author.id) }}
      className="flex gap-3 rounded-md p-3 transition hover:bg-accent/40 focus-visible:outline-2 focus-visible:outline-ring"
    >
      <AuthorAvatar photoPath={author.photo_path} fullName={author.full_name} />
      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex items-center gap-1.5">
          <h3 className="text-base font-medium leading-tight">{author.full_name}</h3>
          {author.is_favorite ? (
            // Подписка на автора — колокольчик (монохром), не «избранное».
            <Bell className="size-3.5 shrink-0 fill-foreground" aria-label="Подписан" />
          ) : null}
          {author.has_adaptations ? (
            <Film className="size-3.5 shrink-0 text-muted-foreground" aria-label="Есть экранизации" />
          ) : null}
        </div>

        <p className="flex flex-wrap items-center gap-x-1 text-sm text-muted-foreground tabular-nums">
          <span>
            {author.book_count} {pluralBooks(author.book_count)} в каталоге
          </span>
          {years ? <span>· {years}</span> : null}
          {author.library_rating != null ? (
            // Рейтинг библиотеки-источника (LIBRATE из INPX, донор) — иконка
            // Landmark (НЕ звезда: звезда строго за избранным; НЕ Library: занята
            // кнопкой «На полку»).
            <span className="inline-flex items-center gap-0.5" aria-label={`Рейтинг библиотеки ${author.library_rating}`}>
              · <Landmark className="size-3 text-muted-foreground" aria-hidden /> {author.library_rating}
            </span>
          ) : null}
          {author.reader_rating != null ? (
            // Оценка читателей этого инстанса (book_ratings) — иконка BookHeart.
            <span
              className="inline-flex items-center gap-0.5"
              aria-label={`Оценка читателей ${author.reader_rating.toFixed(1)} (${author.reader_rating_count ?? 0})`}
            >
              · <BookHeart className="size-3 text-muted-foreground" aria-hidden /> {author.reader_rating.toFixed(1)}
              {author.reader_rating_count ? (
                <span className="text-muted-foreground/70"> ({author.reader_rating_count})</span>
              ) : null}
            </span>
          ) : null}
        </p>

        {author.favorited_books_count > 0 ? (
          <p className="text-xs text-muted-foreground tabular-nums">
            {author.favorited_books_count} {pluralBooks(author.favorited_books_count)} в избранном
          </p>
        ) : null}

        {author.top_genres && author.top_genres.length > 0 ? (
          <div className="flex flex-wrap gap-1 pt-0.5">
            {author.top_genres.map((g) => (
              <Badge key={g.code} variant="secondary" className={chipCls}>
                {genreMap.get(g.code)?.display ?? g.display}
              </Badge>
            ))}
          </div>
        ) : null}

        {langNames.length > 0 ? (
          <p className="text-xs text-muted-foreground">{langNames.join(', ')}</p>
        ) : null}
      </div>
    </Link>
  );
}

/**
 * AuthorAvatar — мини-портрет автора в строке списка. Симметричен
 * AuthorPhoto на карточке, но компактнее (квадрат с иконкой-плейсхолдером).
 */
function AuthorAvatar({ photoPath, fullName }: { photoPath?: string; fullName: string }) {
  const base = 'size-12 shrink-0 overflow-hidden rounded-md border border-border bg-muted';
  if (photoPath) {
    return (
      <img
        src={`/api/covers/${photoPath}`}
        alt={`Фото: ${fullName}`}
        className={cn(base, 'object-cover')}
        loading="lazy"
      />
    );
  }
  return (
    <div
      className={cn(base, 'flex items-center justify-center text-muted-foreground')}
      role="img"
      aria-label={`Фото: ${fullName}`}
    >
      <UserIcon className="size-5 opacity-50" aria-hidden />
    </div>
  );
}

// ── панель фильтров ─────────────────────────────────────────────────

const SORT_OPTIONS: { value: AuthorsFilters['sort']; label: string }[] = [
  { value: 'name', label: 'По имени' },
  { value: 'book_count', label: 'По числу книг' },
  { value: 'rating', label: 'По рейтингу библиотеки' },
  { value: 'reader_rating', label: 'По оценке читателей' },
];

function AuthorsFiltersSidebar({
  value,
  onChange,
  totalActive,
  onReset,
}: {
  value: AuthorsFilters;
  onChange: (next: AuthorsFilters) => void;
  totalActive: number;
  onReset: () => void;
}) {
  const effective = useEffectiveContent();
  const hiddenGenres = effective.data?.hidden_genres;

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

      {/* Сортировка */}
      <div className="space-y-2">
        <div className="text-xs font-medium text-muted-foreground uppercase">Сортировка</div>
        <select
          value={value.sort}
          onChange={(e) => onChange({ ...value, sort: e.target.value as AuthorsFilters['sort'] })}
          aria-label="Сортировка"
          className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm shadow-xs focus-visible:ring-2 focus-visible:ring-ring"
        >
          {SORT_OPTIONS.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      </div>

      {/* Быстрые тумблеры. Иконки — те же, что в строке автора (самообучающий
          UI): подписка = колокольчик, экранизации = плёнка. */}
      <div className="space-y-3">
        <label className="flex items-center justify-between gap-2">
          <span className="flex items-center gap-1.5">
            <Bell className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
            Только избранные
          </span>
          <Switch
            checked={value.favoritesOnly}
            onCheckedChange={(v) => onChange({ ...value, favoritesOnly: v })}
            aria-label="Только избранные авторы"
          />
        </label>
        <label className="flex items-center justify-between gap-2">
          <span className="flex items-center gap-1.5">
            <Film className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
            С экранизациями
          </span>
          <Switch
            checked={value.hasAdaptations}
            onCheckedChange={(v) => onChange({ ...value, hasAdaptations: v })}
            aria-label="Только авторы с экранизациями"
          />
        </label>
      </div>

      {/* Годы активности */}
      <div className="space-y-2">
        <div className="text-xs font-medium text-muted-foreground uppercase">Годы активности</div>
        <div className="flex items-center gap-2">
          <Input
            type="number"
            inputMode="numeric"
            placeholder="от"
            aria-label="Год от"
            value={value.yearFrom || ''}
            min={0}
            max={3000}
            onChange={(e) => onChange({ ...value, yearFrom: parseYear(e.target.value) })}
            className="h-9"
          />
          <span className="text-muted-foreground">—</span>
          <Input
            type="number"
            inputMode="numeric"
            placeholder="до"
            aria-label="Год до"
            value={value.yearTo || ''}
            min={0}
            max={3000}
            onChange={(e) => onChange({ ...value, yearTo: parseYear(e.target.value) })}
            className="h-9"
          />
        </div>
      </div>

      {/* Минимальный рейтинг (библиотечный LIBRATE, 1..5) */}
      <div className="space-y-2">
        <div className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground uppercase">
          <Landmark className="size-3.5 shrink-0" aria-hidden />
          Рейтинг библиотеки от
        </div>
        <select
          value={value.minRating}
          onChange={(e) => onChange({ ...value, minRating: Number(e.target.value) || 0 })}
          aria-label="Минимальный библиотечный рейтинг"
          className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm shadow-xs focus-visible:ring-2 focus-visible:ring-ring"
        >
          <option value={0}>Любой</option>
          <option value={1}>1+</option>
          <option value={2}>2+</option>
          <option value={3}>3+</option>
          <option value={4}>4+</option>
          <option value={5}>5</option>
        </select>
      </div>

      {/* Минимальная средняя оценка читателей (book_ratings, по инстансу). */}
      <div className="space-y-2">
        <div className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground uppercase">
          <BookHeart className="size-3.5 shrink-0" aria-hidden />
          Оценка читателей от
        </div>
        <select
          value={value.minReaderRating}
          onChange={(e) => onChange({ ...value, minReaderRating: Number(e.target.value) || 0 })}
          aria-label="Минимальная оценка читателей"
          className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm shadow-xs focus-visible:ring-2 focus-visible:ring-ring"
        >
          <option value={0}>Любая</option>
          <option value={3}>3+</option>
          <option value={4}>4+</option>
          <option value={4.5}>4.5+</option>
        </select>
      </div>

      {/* Жанры — переиспользуем grouped-фильтр. showCounts=false: числа там
          книжные (book_count), а список — об авторах; author-scoped счётчиков
          пока нет, поэтому не показываем (не вводим в заблуждение). */}
      <GroupedGenresFilter
        selected={value.genres}
        onChange={(genres) => onChange({ ...value, genres })}
        hiddenCodes={hiddenGenres}
        showCounts={false}
      />

      {/* Языки */}
      <LanguagesFilter
        selected={value.langs}
        hiddenCodes={effective.data?.hidden_languages}
        onChange={(langs) => onChange({ ...value, langs })}
      />
    </aside>
  );
}

/**
 * LanguagesFilter — мультиселект языков (чекбоксы). Список языков коллекции
 * из /api/languages; скрытые (admin ∪ персональные) прячем, кроме уже
 * выбранных (чтобы фильтр можно было снять).
 */
function LanguagesFilter({
  selected,
  hiddenCodes,
  onChange,
}: {
  selected: string[];
  hiddenCodes?: string[];
  onChange: (next: string[]) => void;
}) {
  const langsQ = useLanguages();
  const items = useMemo(() => {
    const hidden = new Set(hiddenCodes ?? []);
    const sel = new Set(selected);
    return (langsQ.data ?? []).filter((l) => !hidden.has(l.code) || sel.has(l.code));
  }, [langsQ.data, hiddenCodes, selected]);

  if ((langsQ.data ?? []).length === 0) return null;

  const toggle = (code: string, checked: boolean) => {
    if (checked) onChange([...selected, code]);
    else onChange(selected.filter((c) => c !== code));
  };

  return (
    <div className="space-y-2">
      <div className="text-xs font-medium text-muted-foreground uppercase">Язык</div>
      <ul className="space-y-0.5 max-h-64 overflow-y-auto pr-1">
        {items.map((l) => (
          <li key={l.code}>
            <label className="flex items-center gap-2 cursor-pointer rounded px-1 py-0.5 hover:bg-accent/40">
              <input
                type="checkbox"
                className="size-4 rounded border-input"
                checked={selected.includes(l.code)}
                onChange={(e) => toggle(l.code, e.target.checked)}
              />
              {/* Без числа: book_count — счётчик КНИГ, а список об авторах
                  (и фильтр матчит lang ИЛИ src_lang) — число вводило в
                  заблуждение (4 английских книги ↔ 6 авторов). */}
              <span className="flex-1 truncate text-sm">{l.display}</span>
            </label>
          </li>
        ))}
      </ul>
    </div>
  );
}

// ── helpers ─────────────────────────────────────────────────────────

function AuthorsSkeleton() {
  return (
    <ul className="space-y-1">
      {Array.from({ length: 6 }).map((_, i) => (
        <li key={i} className="flex gap-3 p-3">
          <Skeleton className="size-12 shrink-0 rounded-md" />
          <div className="min-w-0 flex-1 space-y-2 pt-1">
            <Skeleton className="h-4 w-1/2" />
            <Skeleton className="h-3 w-1/4" />
            <Skeleton className="h-3 w-1/3" />
          </div>
        </li>
      ))}
    </ul>
  );
}

function parseYear(raw: string): number {
  const n = Number(raw);
  if (!Number.isFinite(n)) return 0;
  if (n < 0 || n > 3000) return 0;
  return Math.floor(n);
}

function pluralBooks(n: number): string {
  const mod10 = n % 10;
  const mod100 = n % 100;
  if (mod100 >= 11 && mod100 <= 14) return 'книг';
  if (mod10 === 1) return 'книга';
  if (mod10 >= 2 && mod10 <= 4) return 'книги';
  return 'книг';
}

function pluralAuthors(n: number): string {
  const mod10 = n % 10;
  const mod100 = n % 100;
  if (mod100 >= 11 && mod100 <= 14) return 'авторов';
  if (mod10 === 1) return 'автор';
  if (mod10 >= 2 && mod10 <= 4) return 'автора';
  return 'авторов';
}
