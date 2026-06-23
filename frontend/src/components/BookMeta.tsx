import { BookHeart, Check, Film, Globe, Star } from 'lucide-react';
import { useLanguageMap } from '@/lib/content';
import { fmtRating, externalRatingSourceLabel } from '@/lib/ratingDisplay';
import { Tooltip, TooltipTrigger, TooltipContent } from '@/components/ui/tooltip';

// BookMetaFields — структурный набор полей плашки. И BookListItem (/books, автор,
// серия), и CollectionBook (полки) — суперсеты этого набора, поэтому BookMeta
// принимает любой из них без приведения типов.
export type BookMetaFields = {
  year?: number;
  external_rating?: number;
  external_rating_source?: string;
  reader_rating?: number;
  reader_rating_count?: number;
  has_adaptations?: boolean;
  lang?: string;
  is_read?: boolean;
  reading_fraction?: number;
  is_favorite?: boolean;
};

/**
 * BookMeta — компактная строка сигналов книжной плашки (как строка автора в
 * /authors): год · 🌐 внешний рейтинг (Tooltip: источник) · 📖 оценка читателей (N) ·
 * 🎬 экранизация · язык · статус чтения (✓/N%) · ★ избранное. Где можно —
 * пиктограммы (наша ЦА — книжные гики — ценит плотную информативность).
 *
 * Скрывает отсутствующее; возвращает null, если показывать нечего. Переиспользует
 * lib/ratingDisplay (формат + ярлык источника) и Tooltip (снаппи, не нативный
 * title). Иконки монохромные, кроме ★ — жёлтая звезда книжного избранного
 * (исключение из монохрома, как и везде).
 */
export function BookMeta({ book }: { book: BookMetaFields }) {
  const langMap = useLanguageMap();

  const hasReading = book.is_read || (book.reading_fraction != null && book.reading_fraction > 0);
  const nothing =
    book.year == null &&
    book.external_rating == null &&
    book.reader_rating == null &&
    !book.has_adaptations &&
    !book.lang &&
    !hasReading &&
    !book.is_favorite;
  if (nothing) return null;

  const langName = book.lang ? (langMap.get(book.lang) ?? book.lang) : null;
  const pct =
    book.reading_fraction != null ? Math.round(book.reading_fraction * 100) : null;

  return (
    <p className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-muted-foreground tabular-nums">
      {book.year != null ? <span>{book.year}</span> : null}

      {book.external_rating != null ? (
        <Tooltip>
          <TooltipTrigger asChild>
            <span
              className="inline-flex items-center gap-0.5"
              aria-label={`Внешний рейтинг ${fmtRating(book.external_rating)} · ${externalRatingSourceLabel(book.external_rating_source)}`}
            >
              <Globe className="size-3 shrink-0" aria-hidden /> {fmtRating(book.external_rating)}
            </span>
          </TooltipTrigger>
          <TooltipContent>
            Внешний рейтинг {fmtRating(book.external_rating)} ·{' '}
            {externalRatingSourceLabel(book.external_rating_source)}
          </TooltipContent>
        </Tooltip>
      ) : null}

      {book.reader_rating != null ? (
        <span
          className="inline-flex items-center gap-0.5"
          aria-label={`Оценка читателей ${book.reader_rating.toFixed(1)} (${book.reader_rating_count ?? 0})`}
        >
          <BookHeart className="size-3 shrink-0" aria-hidden /> {book.reader_rating.toFixed(1)}
          {book.reader_rating_count ? (
            <span className="text-muted-foreground/70"> ({book.reader_rating_count})</span>
          ) : null}
        </span>
      ) : null}

      {book.has_adaptations ? (
        <Film className="size-3 shrink-0" aria-label="Есть экранизации" />
      ) : null}

      {langName ? <span>{langName}</span> : null}

      {book.is_read ? (
        <span className="inline-flex items-center gap-0.5 text-foreground" aria-label="Прочитано">
          <Check className="size-3 shrink-0" aria-hidden /> прочитано
        </span>
      ) : pct != null && pct > 0 ? (
        <span aria-label={`Прогресс чтения ${pct}%`}>{pct}%</span>
      ) : null}

      {book.is_favorite ? (
        <Star className="size-3 shrink-0 fill-yellow-500 stroke-yellow-500" aria-label="В избранном" />
      ) : null}
    </p>
  );
}
