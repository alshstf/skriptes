import { Link, useParams } from '@tanstack/react-router';
import { BarChart3, ListOrdered } from 'lucide-react';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { BookListItem } from '@/components/BookListItem';
import { BackButton } from '@/components/BackButton';
import { FavoriteButton } from '@/components/FavoriteButton';
import { YearHistogram } from '@/components/YearHistogram';
import { ReadingProgress } from '@/components/ReadingProgress';
import { useSeries, type Series } from '@/lib/catalog';
import { ApiError } from '@/lib/api';

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
        {s.author_name && s.author_id ? (
          <p className="text-sm">
            <span className="text-muted-foreground">Автор:</span>{' '}
            <Link
              to="/authors/$id"
              params={{ id: String(s.author_id) }}
              className="hover:underline"
            >
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
        <ul className="space-y-1">
          {s.books.map((b) => (
            <li key={b.id}>
              <BookListItem book={b} showSeries={false} showSerNo={true} />
            </li>
          ))}
        </ul>
      )}
    </article>
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
              Добавлено по годам
            </div>
            <YearHistogram data={years} />
          </div>
        ) : null}
      </CardContent>
    </Card>
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
