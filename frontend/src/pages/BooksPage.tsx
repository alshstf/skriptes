import { useState } from 'react';
import { Link } from '@tanstack/react-router';
import { Search } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { Button } from '@/components/ui/button';
import { useBooks, type BookListItem } from '@/lib/books';
import { useDebouncedValue } from '@/lib/useDebouncedValue';

const PAGE_SIZE = 20;

export function BooksPage() {
  const [query, setQuery] = useState('');
  const [page, setPage] = useState(0);
  const debouncedQuery = useDebouncedValue(query, 200);

  // Сброс пагинации при смене поискового запроса.
  const effectivePage = debouncedQuery === query ? page : 0;
  const offset = effectivePage * PAGE_SIZE;

  const { data, isLoading, isFetching, error } = useBooks({
    query: debouncedQuery,
    limit: PAGE_SIZE,
    offset,
  });

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-2">
        <div className="relative flex-1 max-w-md">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 size-4 text-muted-foreground" aria-hidden />
          <Input
            type="search"
            placeholder="Поиск по названию или автору"
            className="pl-9"
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setPage(0);
            }}
          />
        </div>
        {data ? (
          <span className="text-sm text-muted-foreground tabular-nums">
            {data.total} {pluralBooks(data.total)} · {data.processing_ms}мс
          </span>
        ) : null}
      </div>

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
          page={effectivePage}
          total={data.total}
          pageSize={PAGE_SIZE}
          onChange={(p) => setPage(p)}
        />
      ) : null}
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
        <Button variant="outline" size="sm" disabled={isFirst} onClick={() => onChange(Math.max(0, page - 1))}>
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
  // Простая русская плюрализация: 1 книга / 2..4 книги / 5+ книг.
  const mod10 = n % 10;
  const mod100 = n % 100;
  if (mod100 >= 11 && mod100 <= 14) return 'книг';
  if (mod10 === 1) return 'книга';
  if (mod10 >= 2 && mod10 <= 4) return 'книги';
  return 'книг';
}
