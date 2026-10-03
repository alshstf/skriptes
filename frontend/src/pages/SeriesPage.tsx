import { useMemo, useState } from 'react';
import { Link, useParams } from '@tanstack/react-router';
import { BarChart3, ListOrdered } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { BookListItem } from '@/components/BookListItem';
import { BackButton } from '@/components/BackButton';
import { FavoriteButton } from '@/components/FavoriteButton';
import { MergeSuggestions } from '@/components/MergeSuggestions';
import { MergeWorksDialog } from '@/components/MergeWorksDialog';
import { YearHistogram } from '@/components/YearHistogram';
import { ReadingProgress } from '@/components/ReadingProgress';
import { useSeries, type Series } from '@/lib/catalog';
import { bySeriesOrder, type BookListItem as Book } from '@/lib/books';
import { ApiError } from '@/lib/api';
import { pluralBooks } from '@/lib/format';


// В шапке — самые плодовитые авторы серии (бэкенд сортирует по числу книг);
// у издательской серии их сотни.
const MAX_HEADER_AUTHORS = 5;

// Большие серии (издательские — до 2,7 тыс. книг, #311) рисуем порциями: весь
// список сразу тяжёл для телефона и неудобен для просмотра.
const PAGE = 100;

type SeriesSort = 'order' | 'year' | 'title';

// В межавторской/издательской серии номер — порядок выпуска у издательства, не
// чтения; там полезнее год или название.
const SORT_OPTIONS: { value: SeriesSort; label: string }[] = [
  { value: 'order', label: 'По номеру' },
  { value: 'year', label: 'По году' },
  { value: 'title', label: 'По названию' },
];

function byYear(a: Book, b: Book): number {
  const ay = a.year ?? Number.POSITIVE_INFINITY;
  const by = b.year ?? Number.POSITIVE_INFINITY;
  return ay !== by ? ay - by : a.title.localeCompare(b.title, 'ru');
}

function byTitle(a: Book, b: Book): number {
  return a.title.localeCompare(b.title, 'ru');
}

const SORTERS: Record<SeriesSort, (a: Book, b: Book) => number> = {
  order: bySeriesOrder,
  year: byYear,
  title: byTitle,
};

export function SeriesPage() {
  const { id } = useParams({ strict: false }) as { id: string };
  const { data: s, isLoading, error } = useSeries(id);

  if (isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-7 w-1/2" />
        <Skeleton className="h-4 w-1/4" />
        <Skeleton className="h-32 w-full" />
      </div>
    );
  }

  if (error) {
    const isNotFound = error instanceof ApiError && error.status === 404;
    return (
      <div className="space-y-3">
        <BackButton />
        <p className="text-sm text-destructive" role="alert">
          {isNotFound ? 'Серия не найдена.' : `Не удалось загрузить: ${error.message}`}
        </p>
      </div>
    );
  }

  if (!s) return null;

  return (
    <article className="space-y-4">
      <BackButton />
      <header className="space-y-2">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight">
            <ListOrdered className="size-5 text-muted-foreground" aria-hidden />
            {s.title}
          </h1>
          <FavoriteButton target="series" id={s.id} isFavorite={s.is_favorite ?? false} />
        </div>
        {s.kind === 'multi' ? (
          <p className="text-sm text-muted-foreground">Межавторская или издательская серия</p>
        ) : null}
        {s.authors && s.authors.length > 0 ? (
          <p className="text-sm">
            <span className="text-muted-foreground">{s.authors.length > 1 ? 'Авторы:' : 'Автор:'}</span>{' '}
            {s.authors.slice(0, MAX_HEADER_AUTHORS).map((a, i) => (
              <span key={a.id}>
                {i > 0 ? ', ' : ''}
                <Link to="/authors/$id" params={{ id: String(a.id) }} className="hover:underline">
                  {a.name}
                </Link>
              </span>
            ))}
            {s.authors.length > MAX_HEADER_AUTHORS ? (
              <span className="text-muted-foreground"> и ещё {s.authors.length - MAX_HEADER_AUTHORS}</span>
            ) : null}
          </p>
        ) : s.author_name && s.author_id ? (
          <p className="text-sm">
            <span className="text-muted-foreground">Автор:</span>{' '}
            <Link to="/authors/$id" params={{ id: String(s.author_id) }} className="hover:underline">
              {s.author_name}
            </Link>
          </p>
        ) : null}
        <p className="text-sm text-muted-foreground tabular-nums">
          {s.book_count} {pluralBooks(s.book_count)} в серии
        </p>
      </header>

      <SeriesStats series={s} />

      {s.books.length === 0 ? (
        <p className="text-sm text-muted-foreground">В серии пока ничего нет.</p>
      ) : (
        <div className="space-y-2">
          {/* Админ-подсказки + ручное объединение (оба сами скрываются у не-админа). */}
          {/* Подсказки «один том — одна книга» опираются на номер тома; в
              межавторской/издательской серии номера у разных книг совпадают —
              подсказки предлагали бы склеить разные романы. */}
          {s.kind !== 'multi' ? <MergeSuggestions books={s.books} /> : null}
          <div className="flex justify-end empty:hidden">
            <MergeWorksDialog books={s.books} />
          </div>
          <SeriesBookList books={s.books} multi={s.kind === 'multi'} />
        </div>
      )}
    </article>
  );
}

// SeriesBookList — книги серии порциями по PAGE; у межавторской серии — выбор
// сортировки (номер, год, название).
function SeriesBookList({ books, multi }: { books: Book[]; multi: boolean }) {
  const [sort, setSort] = useState<SeriesSort>('order');
  const [shown, setShown] = useState(PAGE);
  const sorted = useMemo(() => [...books].sort(SORTERS[sort]), [books, sort]);
  const rest = sorted.length - shown;
  return (
    <>
      {multi && books.length > 1 ? (
        <div className="flex items-center justify-end gap-2 text-sm">
          <label htmlFor="series-sort" className="text-muted-foreground">
            Сортировка
          </label>
          <select
            id="series-sort"
            value={sort}
            onChange={(e) => {
              setSort(e.target.value as SeriesSort);
              setShown(PAGE);
            }}
            className="h-8 rounded-md border border-input bg-background px-2 text-sm shadow-xs focus-visible:ring-2 focus-visible:ring-ring"
          >
            {SORT_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </div>
      ) : null}
      <ul className="space-y-1">
        {sorted.slice(0, shown).map((b) => (
          <li key={b.id}>
            <BookListItem book={b} showSeries={false} showSerNo={true} />
          </li>
        ))}
      </ul>
      {rest > 0 ? (
        <div className="flex flex-col items-center gap-1 pt-2">
          <Button variant="outline" size="sm" onClick={() => setShown((n) => n + PAGE)}>
            Показать ещё {Math.min(PAGE, rest)}
          </Button>
          <span className="text-xs text-muted-foreground tabular-nums">
            Показано {shown} из {sorted.length}
          </span>
        </div>
      ) : null}
    </>
  );
}

// SeriesStats — симметрично AuthorStats. Прячется если нечего показать.
function SeriesStats({ series }: { series: Series }) {
  const years = series.year_stats ?? [];
  const showHistogram = years.length >= 2;
  const showProgress = (series.read_count ?? 0) > 0 && series.book_count > 0;
  if (!showHistogram && !showProgress) return null;
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <BarChart3 className="size-4" aria-hidden /> Статистика
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4 pt-0">
        {showProgress ? (
          <ReadingProgress read={series.read_count ?? 0} total={series.book_count} />
        ) : null}
        {showHistogram ? (
          <div className="space-y-1">
            <div className="text-xs font-medium text-muted-foreground uppercase">
              Книги по годам написания
            </div>
            <YearHistogram data={years} />
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

