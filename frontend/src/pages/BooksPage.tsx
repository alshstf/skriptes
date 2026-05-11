import { useCallback, useState } from 'react';
import { Link, useNavigate, useSearch } from '@tanstack/react-router';
import type { BooksSearch } from '@/router';
import { Search } from 'lucide-react';

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
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { Button } from '@/components/ui/button';
import {
  FiltersSidebar,
  ActiveFilterChips,
  type FiltersValue,
} from '@/components/FiltersSidebar';
import { useBooks, type BookListItem } from '@/lib/books';
import { useDebouncedValue } from '@/lib/useDebouncedValue';

const PAGE_SIZE = 20;
const FACETS = ['genres', 'lang', 'year'];

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

  const page = search.page ?? 0;
  const offset = page * PAGE_SIZE;

  const { data, isLoading, isFetching, error } = useBooks({
    query: debouncedQuery,
    limit: PAGE_SIZE,
    offset,
    genres: filters.genres,
    lang: filters.lang,
    yearFrom: filters.yearFrom,
    yearTo: filters.yearTo,
    seriesId: search.series_id,
    authorId: search.author_id,
    sort: filters.sort,
    facets: FACETS,
  });

  const totalActive =
    filters.genres.length +
    (filters.lang ? 1 : 0) +
    (filters.yearFrom || filters.yearTo ? 1 : 0) +
    (filters.sort ? 1 : 0) +
    (search.series_id ? 1 : 0) +
    (search.author_id ? 1 : 0);

  const resetAll = () => {
    setQueryInput('');
    void navigate({ search: {}, replace: true });
  };

  return (
    <div className="grid gap-6 md:grid-cols-[260px_minmax(0,1fr)]">
      <div>
        <FiltersSidebar
          value={filters}
          onChange={setFilters}
          facets={data?.facets}
          totalActive={totalActive}
          onReset={resetAll}
        />
      </div>

      <div className="space-y-4">
        <div className="flex flex-wrap items-center gap-2">
          <div className="relative flex-1 max-w-md">
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
          {data ? (
            <span className="text-sm text-muted-foreground tabular-nums">
              {data.total} {pluralBooks(data.total)} · {data.processing_ms}мс
            </span>
          ) : null}
        </div>

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

        {error ? (
          <p role="alert" className="text-sm text-destructive">
            Не удалось загрузить список: {(error as Error).message}
          </p>
        ) : null}

        {isLoading ? (
          <BookListSkeleton />
        ) : data && data.items.length === 0 ? (
          <p className="text-sm text-muted-foreground">Ничего не нашлось.</p>
        ) : data ? (
          <ul className={`space-y-3 ${isFetching ? 'opacity-70' : ''}`}>
            {data.items.map((b) => (
              <li key={b.id}>
                <BookCard book={b} />
              </li>
            ))}
          </ul>
        ) : null}

        {data && data.total > PAGE_SIZE ? (
          <Pagination
            page={page}
            total={data.total}
            pageSize={PAGE_SIZE}
            onChange={(p) =>
              void navigate({
                search: (prev) => ({ ...prev, page: p > 0 ? p : undefined }),
                replace: true,
              })
            }
          />
        ) : null}
      </div>
    </div>
  );
}

function BookCard({ book }: { book: BookListItem }) {
  return (
    <Link
      to="/books/$id"
      params={{ id: String(book.id) }}
      className="block rounded-md transition hover:bg-accent/40 focus-visible:outline-2 focus-visible:outline-ring"
    >
      <Card className="border-transparent bg-transparent shadow-none">
        <CardHeader className="pb-2">
          <CardTitle className="text-base font-medium leading-tight">{book.title}</CardTitle>
        </CardHeader>
        <CardContent className="pt-0 space-y-1">
          {book.authors && book.authors.length > 0 ? (
            <p className="text-sm text-muted-foreground">{book.authors.join(', ')}</p>
          ) : null}
          {book.series ? (
            <p className="text-xs text-muted-foreground">Серия: {book.series}</p>
          ) : null}
          {book.genres && book.genres.length > 0 ? (
            <div className="flex flex-wrap gap-1 pt-1">
              {book.genres.map((g) => (
                <Badge key={g} variant="secondary" className="text-xs font-normal">
                  {g}
                </Badge>
              ))}
            </div>
          ) : null}
        </CardContent>
      </Card>
    </Link>
  );
}

function BookListSkeleton() {
  return (
    <ul className="space-y-3">
      {Array.from({ length: 5 }).map((_, i) => (
        <li key={i} className="space-y-2 rounded-md border border-border p-4">
          <Skeleton className="h-4 w-2/3" />
          <Skeleton className="h-3 w-1/3" />
          <Skeleton className="h-3 w-1/4" />
        </li>
      ))}
    </ul>
  );
}

function Pagination({
  page,
  total,
  pageSize,
  onChange,
}: {
  page: number;
  total: number;
  pageSize: number;
  onChange: (p: number) => void;
}) {
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const isFirst = page === 0;
  const isLast = page >= totalPages - 1;
  return (
    <div className="flex items-center justify-between text-sm">
      <span className="text-muted-foreground">
        Страница {page + 1} из {totalPages}
      </span>
      <div className="flex gap-2">
        <Button
          variant="outline"
          size="sm"
          disabled={isFirst}
          onClick={() => onChange(Math.max(0, page - 1))}
        >
          Назад
        </Button>
        <Button variant="outline" size="sm" disabled={isLast} onClick={() => onChange(page + 1)}>
          Вперёд
        </Button>
      </div>
    </div>
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
