import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useWindowVirtualizer } from '@tanstack/react-virtual';
import { Link, useNavigate, useSearch } from '@tanstack/react-router';
import type { BooksSearch } from '@/router';
import { FilterX, Search, SlidersHorizontal } from 'lucide-react';

// Code-based routing в TanStack Router не разносит validateSearch-тип
// через routeTree-тайпинг, поэтому navigate-функция оказывается типа
// "search не может быть ничем кроме never". Заворачиваем в свой
// helper-тип, который принимает BooksSearch — реально search-параметры
// проверяются validateSearch'ом ниже в run-time.
type BooksNavigate = (opts: {
  search?: BooksSearch | ((prev: BooksSearch) => BooksSearch);
  replace?: boolean;
}) => void;
import { Input } from '@/components/ui/input';
import { Badge } from '@/components/ui/badge';
import { BookCover } from '@/components/BookCover';
import { GenreChips } from '@/components/GenreChips';
import { BookMeta } from '@/components/BookMeta';
import { Skeleton } from '@/components/ui/skeleton';
import { Button } from '@/components/ui/button';
import {
  FiltersSidebar,
  ActiveFilterChips,
  type FiltersValue,
} from '@/components/FiltersSidebar';
import {
  Sheet,
  SheetContent,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from '@/components/ui/sheet';
import { useInfiniteBooks, type BookListItem } from '@/lib/books';
import { useDebouncedValue } from '@/lib/useDebouncedValue';

const PAGE_SIZE = 20;
const FACETS = ['genres', 'lang', 'src_lang', 'year'];

export function BooksPage() {
  // Все фильтры живут в URL-search → удобно делиться ссылками и refresh
  // ничего не теряет. Тип BooksSearch гарантируется validateSearch в
  // router.tsx; useSearch на code-based роутах в strict mode не
  // выводит его — поэтому явная аннотация.
  // strict:false — на code-based роутах TanStack не разносит
  // validateSearch-тип, нам это и не нужно: search мы валидируем сами
  // через as BooksSearch (рантайм-форму гарантирует router.tsx).
  const search = useSearch({ strict: false }) as BooksSearch;
  const navigate = useNavigate() as unknown as BooksNavigate;

  // Поисковый ввод — локальный стейт с debounce, чтобы не перерисовывать
  // URL на каждое нажатие. URL обновляем после паузы.
  const [queryInput, setQueryInput] = useState(search.q ?? '');
  const debouncedQuery = useDebouncedValue(queryInput, 200);

  // Мобильный drawer фильтров (на десктопе фильтры — sidebar, тут стейт
  // не используется, Sheet рендерится только в md:hidden-обёртке).
  const [filtersOpen, setFiltersOpen] = useState(false);

  // Синхронизируем URL.q ← debouncedQuery когда они разъезжаются.
  if (debouncedQuery !== (search.q ?? '') && debouncedQuery === queryInput) {
    void navigate({
      search: (prev) => ({
        ...prev,
        q: debouncedQuery || undefined,
        page: undefined,
      }),
      replace: true,
    });
  }

  const filters: FiltersValue = {
    genres: search.genres ?? [],
    lang: search.lang ?? '',
    srcLang: search.src_lang ?? '',
    yearFrom: search.year_from ?? 0,
    yearTo: search.year_to ?? 0,
    sort: search.sort ?? '',
  };

  const setFilters = useCallback(
    (next: FiltersValue) => {
      void navigate({
        search: (prev) => ({
          ...prev,
          genres: next.genres.length > 0 ? next.genres : undefined,
          lang: next.lang || undefined,
          src_lang: next.srcLang || undefined,
          year_from: next.yearFrom || undefined,
          year_to: next.yearTo || undefined,
          sort: next.sort || undefined,
          page: undefined,
        }),
        replace: true,
      });
    },
    [navigate],
  );

  const {
    data,
    isLoading,
    isFetching,
    error,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
  } = useInfiniteBooks({
    query: debouncedQuery,
    limit: PAGE_SIZE,
    genres: filters.genres,
    lang: filters.lang,
    srcLang: filters.srcLang,
    yearFrom: filters.yearFrom,
    yearTo: filters.yearTo,
    seriesId: search.series_id,
    authorId: search.author_id,
    sort: filters.sort,
    facets: FACETS,
  });

  // Бесконечная прокрутка: items со всех загруженных страниц; total и
  // facets — из первой (они про весь запрос). meta — первая страница.
  const firstPage = data?.pages[0];
  const items = data?.pages.flatMap((p) => p.items) ?? [];

  // Виртуализация: на коллекции 500k бесконечный скролл без неё раздувает
  // DOM. Оконный виртуализатор (useWindowVirtualizer) — чтобы сохранить
  // window-scroll, sticky-бар и двухколоночную вёрстку (контейнерный
  // скролл их бы сломал). Высоты карточек разные → measureElement меряет
  // фактическую. scrollMargin — offsetTop списка (под баром/счётчиком/
  // чипсами), точка отсчёта оконных координат; пересчитываем при сдвиге
  // контента выше.
  const listRef = useRef<HTMLDivElement>(null);
  const [scrollMargin, setScrollMargin] = useState(0);
  // Без deps намеренно: пересчитываем offsetTop на каждом рендере (контент
  // выше — счётчик, чипсы — может менять высоту). setState с тем же
  // значением React бейлит → цикла нет.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useLayoutEffect(() => {
    const top = listRef.current?.offsetTop ?? 0;
    setScrollMargin((prev) => (prev !== top ? top : prev));
  });

  const virtualizer = useWindowVirtualizer({
    count: items.length,
    estimateSize: () => 96,
    overscan: 8,
    scrollMargin,
    getItemKey: (index) => items[index]?.id ?? index,
  });
  const virtualItems = virtualizer.getVirtualItems();

  // Автоподгрузка: как только виртуализатор дорендерил до конца уже
  // загруженного — тянем следующую страницу (overscan даёт префетч).
  useEffect(() => {
    const last = virtualItems[virtualItems.length - 1];
    if (last && last.index >= items.length - 1 && hasNextPage && !isFetchingNextPage) {
      void fetchNextPage();
    }
  }, [virtualItems, items.length, hasNextPage, isFetchingNextPage, fetchNextPage]);

  const totalActive =
    filters.genres.length +
    (filters.lang ? 1 : 0) +
    (filters.srcLang ? 1 : 0) +
    (filters.yearFrom || filters.yearTo ? 1 : 0) +
    (filters.sort ? 1 : 0) +
    (search.series_id ? 1 : 0) +
    (search.author_id ? 1 : 0);

  const resetAll = () => {
    setQueryInput('');
    void navigate({ search: {}, replace: true });
  };

  return (
    <div className="grid grid-cols-1 gap-6 md:grid-cols-[260px_minmax(0,1fr)]">
      {/* Десктоп: фильтры — постоянный sidebar. На мобильном прячем
          (md:block), там фильтры живут в drawer по кнопке ниже. */}
      <div className="hidden md:block">
        <FiltersSidebar
          value={filters}
          onChange={setFilters}
          facets={firstPage?.facets}
          totalActive={totalActive}
          onReset={resetAll}
        />
      </div>

      <div className="space-y-4">
        {/* Sticky-бар управления: поиск + сброс + фильтры. На мобильном
            липнет под шапкой (top-14 = высота Header'а h-14), чтобы при
            скролле вниз контролы оставались под рукой и не нужно было
            мотать наверх. На десктопе sticky выключаем — там фильтры
            живут в постоянном sidebar. -mx-4/px-4 даёт фон бара на всю
            ширину под gutter'ом, чтобы карточки не «просвечивали» по краям
            при скролле под баром. */}
        <div className="sticky top-[calc(env(safe-area-inset-top)+3.5rem)] z-10 -mx-4 bg-background px-4 py-2 md:static md:top-auto md:z-auto md:mx-0 md:bg-transparent md:px-0 md:py-0">
          <div className="flex items-center gap-2">
            <div className="relative flex-1 md:max-w-md">
              <Search
                className="absolute left-3 top-1/2 -translate-y-1/2 size-4 text-muted-foreground"
                aria-hidden
              />
              <Input
                type="search"
                placeholder="Поиск по названию или автору"
                className="pl-9"
                value={queryInput}
                onChange={(e) => setQueryInput(e.target.value)}
              />
            </div>

            {/* Быстрый сброс фильтров — только мобильный и только когда
                есть что сбрасывать. На десктопе сброс живёт в шапке
                sidebar'а. */}
            {totalActive > 0 ? (
              <Button
                variant="ghost"
                size="icon"
                aria-label="Сбросить фильтры"
                onClick={resetAll}
                className="shrink-0 text-muted-foreground md:hidden"
              >
                <FilterX className="size-4" aria-hidden />
              </Button>
            ) : null}

            {/* Мобильная кнопка фильтров — только < md. Бейдж со счётчиком
                активных фильтров, чтобы было видно что что-то применено
                даже со свёрнутой панелью. */}
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
            <SheetContent
              side="left"
              className="w-[85%] gap-0 p-0"
              // Не фокусируем первое поле (сортировку) при открытии — иначе на
              // мобильном раскрывался её дропдаун и перекрывал треть дровера.
              onOpenAutoFocus={(e) => e.preventDefault()}
            >
              {/* SheetTitle обязателен для a11y (Radix варнит без него),
                  но визуально дублировал бы собственный заголовок
                  FiltersSidebar — поэтому sr-only. */}
              <SheetHeader className="sr-only">
                <SheetTitle>Фильтры</SheetTitle>
              </SheetHeader>
              <div className="flex-1 overflow-y-auto p-4 pt-12">
                <FiltersSidebar
                  value={filters}
                  onChange={setFilters}
                  facets={firstPage?.facets}
                  totalActive={totalActive}
                  onReset={resetAll}
                />
              </div>
              <SheetFooter className="border-t">
                <Button onClick={() => setFiltersOpen(false)}>
                  {firstPage
                    ? `Показать ${firstPage.total.toLocaleString('ru-RU')} ${pluralBooks(firstPage.total)}`
                    : 'Показать'}
                </Button>
              </SheetFooter>
            </SheetContent>
          </Sheet>
          </div>
        </div>

        {firstPage ? (
          <p className="text-sm text-muted-foreground tabular-nums">
            {firstPage.total.toLocaleString('ru-RU')} {pluralBooks(firstPage.total)} ·{' '}
            {firstPage.processing_ms}мс
          </p>
        ) : null}

        {/* Чипсы выбранных фильтров — только на десктопе. На мобильном
            при выборе целой категории жанров их набегает столько, что
            блок съедает пол-экрана до первой книги; роль «что выбрано»
            там берут бейдж-счётчик на кнопке фильтра + быстрый сброс. */}
        <div className="hidden md:block">
          <ActiveFilterChips
            value={{
              ...filters,
              seriesId: search.series_id,
              authorId: search.author_id,
            }}
            onChange={(next) => {
              const { seriesId, authorId, ...rest } = next;
              setFilters(rest);
              // Series/author не в FiltersValue — обновляем отдельно.
              void navigate({
                search: (prev) => ({
                  ...prev,
                  series_id: seriesId || undefined,
                  author_id: authorId || undefined,
                }),
                replace: true,
              });
            }}
          />
        </div>

        {error ? (
          <p role="alert" className="text-sm text-destructive">
            Не удалось загрузить список: {(error as Error).message}
          </p>
        ) : null}

        {isLoading ? (
          <BookListSkeleton />
        ) : firstPage && items.length === 0 ? (
          <p className="text-sm text-muted-foreground">Ничего не нашлось.</p>
        ) : firstPage ? (
          <>
            {/* Виртуализированный список: контейнер высотой во весь набор,
                строки позиционируются абсолютно по измеренным высотам.
                opacity только на рефетч после смены фильтра, не на
                подгрузку следующей страницы (иначе мигает весь список). */}
            <div
              ref={listRef}
              className={isFetching && !isFetchingNextPage ? 'opacity-70' : ''}
              style={{ height: `${virtualizer.getTotalSize()}px`, position: 'relative' }}
            >
              {virtualItems.map((vi) => (
                <div
                  key={vi.key}
                  data-index={vi.index}
                  ref={virtualizer.measureElement}
                  className="pb-3"
                  style={{
                    position: 'absolute',
                    top: 0,
                    left: 0,
                    width: '100%',
                    transform: `translateY(${vi.start - scrollMargin}px)`,
                  }}
                >
                  <BookCard book={items[vi.index]} highlightGenres={filters.genres} />
                </div>
              ))}
            </div>

            {hasNextPage ? (
              <div className="pt-2">
                <Button
                  variant="outline"
                  className="w-full"
                  onClick={() => void fetchNextPage()}
                  disabled={isFetchingNextPage}
                >
                  {isFetchingNextPage ? 'Загрузка…' : 'Показать ещё'}
                </Button>
              </div>
            ) : items.length > 0 ? (
              <p className="pt-2 text-center text-xs text-muted-foreground">Это все книги</p>
            ) : null}
          </>
        ) : null}
      </div>
    </div>
  );
}

function BookCard({
  book,
  highlightGenres,
}: {
  book: BookListItem;
  highlightGenres?: string[];
}) {
  // Вся карточка кликабельна и ведёт на деталку — паттерн «stretched
  // link»: ссылка висит на заголовке, а её ::after растягивается на всю
  // площадь карточки (родитель relative). Это позволяет держать внутри
  // интерактивный «островок» (поповер «+N» в GenreChips) без вложения
  // <button> в <a> — недопустимого и ломающего навигацию. «+N» поднят
  // над ::after через relative z-10.
  return (
    <div className="relative flex gap-3 rounded-md p-2 transition hover:bg-accent/40 sm:p-3">
      <BookCover
        src={`/api/covers/book/${book.cover_edition_id ?? book.id}`}
        title={book.title}
        placeholder="monogram"
        className="w-12 sm:w-14"
      />
      <div className="min-w-0 flex-1 space-y-0.5">
        <h3 className="font-medium leading-snug line-clamp-2">
          <Link
            to="/works/$id"
            params={{ id: String(book.work_id ?? book.id) }}
            className="rounded-md after:absolute after:inset-0 focus-visible:outline-2 focus-visible:outline-ring"
          >
            {book.title}
          </Link>
        </h3>
        {book.edition_count && book.edition_count > 1 ? (
          <p className="text-xs text-muted-foreground tabular-nums">{book.edition_count} изданий</p>
        ) : null}
        {book.authors && book.authors.length > 0 ? (
          <p className="text-sm text-muted-foreground line-clamp-1">{book.authors.join(', ')}</p>
        ) : null}
        {book.series ? (
          <p className="text-xs text-muted-foreground line-clamp-1">Серия: {book.series}</p>
        ) : null}
        {book.genres && book.genres.length > 0 ? (
          <GenreChips genres={book.genres} highlight={highlightGenres} />
        ) : null}
        <BookMeta book={book} />
      </div>
    </div>
  );
}

function BookListSkeleton() {
  // Зеркалит горизонтальный layout BookCard: thumbnail-обложка слева +
  // строки текста справа — чтобы при подмене skeleton'а на данные не
  // было layout-сдвига.
  return (
    <ul className="space-y-3">
      {Array.from({ length: 5 }).map((_, i) => (
        <li key={i} className="flex gap-3 p-2 sm:p-3">
          <Skeleton className="aspect-[2/3] w-12 shrink-0 rounded-md sm:w-14" />
          <div className="min-w-0 flex-1 space-y-2 pt-1">
            <Skeleton className="h-4 w-2/3" />
            <Skeleton className="h-3 w-1/3" />
            <Skeleton className="h-3 w-1/4" />
          </div>
        </li>
      ))}
    </ul>
  );
}

function pluralBooks(n: number): string {
  const mod10 = n % 10;
  const mod100 = n % 100;
  if (mod100 >= 11 && mod100 <= 14) return 'книг';
  if (mod10 === 1) return 'книга';
  if (mod10 >= 2 && mod10 <= 4) return 'книги';
  return 'книг';
}
