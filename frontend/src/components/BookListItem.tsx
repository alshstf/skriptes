import { Link } from '@tanstack/react-router';
import { Badge } from '@/components/ui/badge';
import type { BookListItem as Item } from '@/lib/books';

/**
 * BookListItem — компактная строка книги в любом списке
 * (BooksPage / AuthorPage / SeriesPage). Кликабельна целиком.
 */
export function BookListItem({ book, showSeries = true }: { book: Item; showSeries?: boolean }) {
  return (
    <Link
      to="/books/$id"
      params={{ id: String(book.id) }}
      className="block rounded-md p-3 transition hover:bg-accent/40 focus-visible:outline-2 focus-visible:outline-ring"
    >
      <div className="space-y-1">
        <h3 className="text-base font-medium leading-tight">{book.title}</h3>
        {book.authors && book.authors.length > 0 ? (
          <p className="text-sm text-muted-foreground">{book.authors.join(', ')}</p>
        ) : null}
        {showSeries && book.series ? (
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
      </div>
    </Link>
  );
}
