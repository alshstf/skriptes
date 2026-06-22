/**
 * lib/ratingDisplay.ts — общие хелперы отображения ВНЕШНЕГО рейтинга
 * (LIBRATE ∪ web). Используются и на карточке книги, и в списке/карточке автора,
 * чтобы формат значения и подпись источника были одинаковыми везде.
 */

// fmtRating — LIBRATE целочисленный, web/инстанс — дробные: целые показываем
// без хвоста, иначе один знак после запятой.
export function fmtRating(v: number): string {
  return Number.isInteger(v) ? String(v) : v.toFixed(1);
}

// externalRatingSourceLabel — человекочитаемый источник внешнего рейтинга.
// 'library' — донорская библиотека (LIBRATE из INPX); остальные — web.
export function externalRatingSourceLabel(source?: string): string {
  switch (source) {
    case 'library':
      return 'библиотека';
    case 'googlebooks':
    case 'google_books':
      return 'Google Books';
    case 'openlibrary':
      return 'OpenLibrary';
    default:
      return 'внешний источник';
  }
}
