import { Link } from '@tanstack/react-router';
import { Badge } from '@/components/ui/badge';
import { BookMeta } from '@/components/BookMeta';
import { useGenreMap } from '@/lib/genres';
import { useGenreChipStyle, genreChipClass } from '@/lib/appearance';
import type { BookListItem as Item } from '@/lib/books';

/**
 * BookListItem — компактная строка книги в любом списке
 * (BooksPage / AuthorPage / SeriesPage). Кликабельна целиком.
 *
 * showSerNo: если true и у книги есть `ser_no`, слева от заголовка
 * рендерим колонку с номером тома (`1.`, `2.` …). Используется внутри
 * карточки серии на странице автора и на странице самой серии.
 */
export function BookListItem({
  book,
  showSeries = true,
  showSerNo = false,
}: {
  book: Item;
  showSeries?: boolean;
  showSerNo?: boolean;
}) {
  const serNo = showSerNo && typeof book.ser_no === 'number' ? book.ser_no : null;
  // book.genres приходит из Meili-индекса как массив fb2_code'ов
  // (см. internal/search/index.go). Здесь переводим в человеческие
  // display-имена через useGenreMap. Если справочник ещё в полёте
  // или код не в словаре — показываем сам код как fallback.
  const genreMap = useGenreMap();
  const chipCls = genreChipClass(useGenreChipStyle());
  return (
    <Link
      to="/works/$id"
      params={{ id: String(book.work_id ?? book.id) }}
      className="flex gap-3 rounded-md p-3 transition hover:bg-accent/40 focus-visible:outline-2 focus-visible:outline-ring"
    >
      {serNo != null ? (
        // min-w (не фикс. w-6): у некоторых серий ser_no = ГОД (1996, 1998 —
        // «Антология фантастики»); 4 цифры в 24px упирались в название. min-width
        // держит выравнивание для 1–2 значных номеров и растёт для широких, не
        // съедая gap-3 до заголовка. mr-0.5 — небольшой зазор сверх gap.
        <span
          aria-label={`Том ${serNo}`}
          className="min-w-[1.75rem] shrink-0 pt-0.5 text-right text-sm font-medium tabular-nums text-muted-foreground"
        >
          {serNo}.
        </span>
      ) : null}
      <div className="space-y-1 min-w-0 flex-1">
        <h3 className="text-base font-medium leading-tight">
          {book.title}
          {book.edition_count && book.edition_count > 1 ? (
            <span className="ml-2 align-middle text-xs font-normal text-muted-foreground tabular-nums">
              · {book.edition_count} изд.
            </span>
          ) : null}
        </h3>
        {book.authors && book.authors.length > 0 ? (
          <p className="text-sm text-muted-foreground">{book.authors.join(', ')}</p>
        ) : null}
        {showSeries && book.series ? (
          <p className="text-xs text-muted-foreground">Серия: {book.series}</p>
        ) : null}
        {book.genres && book.genres.length > 0 ? (
          <div className="flex flex-wrap gap-1 pt-1">
            {book.genres.map((g) => (
              <Badge key={g} variant="secondary" className={chipCls}>
                {genreMap.get(g)?.display ?? g}
              </Badge>
            ))}
          </div>
        ) : null}
        <BookMeta book={book} />
      </div>
    </Link>
  );
}
