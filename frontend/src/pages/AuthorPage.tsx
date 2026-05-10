import { Link, useParams } from '@tanstack/react-router';
import { BookOpen, ListOrdered } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { BookListItem } from '@/components/BookListItem';
import { BackButton } from '@/components/BackButton';
import { useAuthor } from '@/lib/catalog';
import { ApiError } from '@/lib/api';

export function AuthorPage() {
  const { id } = useParams({ strict: false }) as { id: string };
  const { data: a, isLoading, error } = useAuthor(id);

  if (isLoading) return <AuthorSkeleton />;

  if (error) {
    const isNotFound = error instanceof ApiError && error.status === 404;
    return (
      <div className="space-y-3">
        <BackButton />
        <p className="text-sm text-destructive" role="alert">
          {isNotFound ? 'Автор не найден.' : `Не удалось загрузить: ${error.message}`}
        </p>
      </div>
    );
  }

  if (!a) return null;

  return (
    <article className="space-y-6">
      <BackButton />
      <header className="space-y-2">
        <h1 className="text-2xl font-semibold tracking-tight">{a.full_name}</h1>
        <p className="text-sm text-muted-foreground">
          {a.book_count} {pluralBooks(a.book_count)} в каталоге
        </p>
        {a.top_genres && a.top_genres.length > 0 ? (
          <div className="flex flex-wrap gap-1 pt-1">
            {a.top_genres.map((g) => (
              <Badge key={g.code} variant="secondary" className="font-normal">
                {g.display} · {g.count}
              </Badge>
            ))}
          </div>
        ) : null}
      </header>

      {a.series && a.series.length > 0 ? (
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="flex items-center gap-2 text-base">
              <ListOrdered className="size-4" aria-hidden /> Серии
            </CardTitle>
          </CardHeader>
          <CardContent className="pt-0">
            <ul className="divide-y divide-border">
              {a.series.map((s) => (
                <li key={s.id}>
                  <Link
                    to="/series/$id"
                    params={{ id: String(s.id) }}
                    className="flex items-center justify-between py-2 hover:underline"
                  >
                    <span>{s.title}</span>
                    <span className="text-sm text-muted-foreground tabular-nums">
                      {s.count} {pluralBooks(s.count)}
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
      ) : null}

      <section className="space-y-2">
        <h2 className="flex items-center gap-2 text-base font-medium">
          <BookOpen className="size-4" aria-hidden /> Книги
          {a.books_total > a.books.length ? (
            <span className="text-sm font-normal text-muted-foreground">
              (показаны последние {a.books.length} из {a.books_total})
            </span>
          ) : null}
        </h2>
        {a.books.length === 0 ? (
          <p className="text-sm text-muted-foreground">Книг нет.</p>
        ) : (
          <ul className="space-y-1">
            {a.books.map((b) => (
              <li key={b.id}>
                <BookListItem book={b} showSeries={true} />
              </li>
            ))}
          </ul>
        )}
      </section>
    </article>
  );
}

function AuthorSkeleton() {
  return (
    <div className="space-y-4">
      <Skeleton className="h-7 w-1/2" />
      <Skeleton className="h-4 w-1/4" />
      <Skeleton className="h-32 w-full" />
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
