import { Link } from '@tanstack/react-router';
import { ChevronRight, UserRound } from 'lucide-react';
import type { MatchedAuthor } from '@/lib/books';
import { pluralBooks } from '@/lib/format';

/**
 * MatchedAuthors — плашка над выдачей /books, когда запрос — имя известного
 * автора (#290): его работы и так идут первыми, а отсюда — переход на карточку
 * автора со всеми книгами, сериями и биографией.
 */
export function MatchedAuthors({ authors }: { authors: MatchedAuthor[] }) {
  if (authors.length === 0) return null;
  return (
    <nav aria-label="Авторы по запросу" className="flex flex-wrap gap-2">
      {authors.map((a) => (
        <Link
          key={a.id}
          to="/authors/$id"
          params={{ id: String(a.id) }}
          className="inline-flex min-w-0 max-w-full items-center gap-2 rounded-md border border-border bg-muted/40 px-3 py-2 text-sm transition-colors hover:bg-muted"
        >
          <UserRound className="size-4 shrink-0 text-muted-foreground" aria-hidden />
          <span className="truncate font-medium">{a.full_name}</span>
          {a.note ? <span className="shrink-0 text-muted-foreground">{a.note}</span> : null}
          <span className="shrink-0 tabular-nums text-muted-foreground">
            · {a.book_count.toLocaleString('ru-RU')} {pluralBooks(a.book_count)}
          </span>
          <ChevronRight className="size-4 shrink-0 text-muted-foreground" aria-hidden />
        </Link>
      ))}
    </nav>
  );
}

