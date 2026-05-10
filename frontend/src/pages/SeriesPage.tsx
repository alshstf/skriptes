import { Link, useParams } from '@tanstack/react-router';
import { ArrowLeft, ListOrdered } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { BookListItem } from '@/components/BookListItem';
import { useSeries } from '@/lib/catalog';
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
        <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight">
          <ListOrdered className="size-5 text-muted-foreground" aria-hidden />
          {s.title}
        </h1>
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

      {s.books.length === 0 ? (
        <p className="text-sm text-muted-foreground">В серии пока ничего нет.</p>
      ) : (
        <ul className="space-y-1">
          {s.books.map((b) => (
            <li key={b.id}>
              <BookListItem book={b} showSeries={false} />
            </li>
          ))}
        </ul>
      )}
    </article>
  );
}

function BackButton() {
  return (
    <Button asChild variant="ghost" size="sm">
      <Link to="/books">
        <ArrowLeft className="mr-2 size-4" aria-hidden />К списку книг
      </Link>
    </Button>
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
