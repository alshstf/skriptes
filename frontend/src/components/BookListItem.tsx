import { useContext } from 'react';
import { Link } from '@tanstack/react-router';
import { BookCover } from '@/components/BookCover';
import { BookMeta } from '@/components/BookMeta';
import { GenreChips } from '@/components/GenreChips';
import { ContextAuthorName, pluralAuthors, summarizeAuthors, type BookListItem as Item } from '@/lib/books';

/**
 * BookListItem — компактная строка книги в списках (AuthorPage / SeriesPage /
 * премии / полки). Кликабельна целиком — паттерн «stretched link», как
 * BookCard на /books: ссылка на заголовке, её ::after растянут на строку
 * (родитель relative). Так внутри живёт «+N» жанров (GenreChips) без
 * вложения <button> в <a> (#518).
 *
 * showSerNo: если true и у книги есть `ser_no`, слева от заголовка
 * рендерим колонку с номером тома (`1.`, `2.` …). Используется внутри
 * карточки серии на странице автора и на странице самой серии.
 *
 * showCover: миниатюра обложки слева (как в списке /books): карточки автора и
 * серии (#518), лауреаты премий.
 */
export function BookListItem({
  book,
  showSeries = true,
  showSerNo = false,
  showCover = false,
  hash,
}: {
  book: Item;
  showSeries?: boolean;
  showSerNo?: boolean;
  showCover?: boolean;
  /** Якорь на карточке книги (подборка «Экранизации» → #adaptations). */
  hash?: string;
}) {
  const serNo = showSerNo && typeof book.ser_no === 'number' ? book.ser_no : null;
  // Сборник десятков авторов — не «портянка» на экран: 3 имени и «и ещё N» (#449);
  // полный список — в подсказке и на карточке книги.
  const authorsLine = summarizeAuthors(book.authors ?? [], useContext(ContextAuthorName));
  return (
    <div className="relative flex gap-3 rounded-md p-3 transition hover:bg-accent/40">
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
      {showCover ? (
        <BookCover
          src={`/api/covers/book/${book.cover_edition_id ?? book.id}`}
          title={book.title}
          placeholder="monogram"
          className="w-12 sm:w-14"
        />
      ) : null}
      <div className="space-y-1 min-w-0 flex-1">
        <h3 className="text-base font-medium leading-tight">
          <Link
            to="/works/$id"
            params={{ id: String(book.work_id ?? book.id) }}
            hash={hash}
            className="rounded-md after:absolute after:inset-0 focus-visible:outline-2 focus-visible:outline-ring"
          >
            {book.title}
          </Link>
          {book.edition_count && book.edition_count > 1 ? (
            <span className="ml-2 align-middle text-xs font-normal text-muted-foreground tabular-nums">
              · {book.edition_count} изд.
            </span>
          ) : null}
        </h3>
        {authorsLine.shown.length > 0 ? (
          <p
            className="text-sm text-muted-foreground"
            title={authorsLine.more > 0 ? book.authors.join(', ') : undefined}
          >
            {authorsLine.shown.join(', ')}
            {authorsLine.more > 0 ? ` и ещё ${authorsLine.more} ${pluralAuthors(authorsLine.more)}` : ''}
          </p>
        ) : null}
        {showSeries && book.series ? (
          <p className="text-xs text-muted-foreground">Серия: {book.series}</p>
        ) : null}
        {/* Коды fb2 → display-имена внутри GenreChips; не влезшие в строку — «+N». */}
        {book.genres && book.genres.length > 0 ? <GenreChips genres={book.genres} /> : null}
        <BookMeta book={book} />
      </div>
    </div>
  );
}
