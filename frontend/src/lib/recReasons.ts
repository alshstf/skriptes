import type { RecReason } from './recs';
import { formatReadingTime, formatRelativeDays, pluralRu } from './format';

// pluralBooksOf — «1 книгу / 2 книги / 5 книг» (винительный падеж: «полюбили N книг»).
function pluralBooksOf(n: number): string {
  return pluralRu(n, ['книгу', 'книги', 'книг']);
}

/**
 * recReasons.ts — текст причины рекомендации по её виду (контракт recsys.Reason).
 * Чистые функции без React: компонент RecReasons (components/RecReason.tsx)
 * превращает части в строку со ссылками. Обращение — на «вы», как везде в UI.
 */

// ReasonPart — кусок текста причины; link — ссылка на автора/цикл.
export type ReasonPart = { text: string; link?: { to: '/authors/$id' | '/series/$id'; id: number } };

/** ReasonContext — что нужно тексту помимо причины: имя жанра по fb2-коду (useGenreMap). */
export type ReasonContext = { genreName?: (code: string) => string | undefined };

/** reasonParts — части текста причины; пустой массив — причину не показываем. */
export function reasonParts(r: RecReason, now = new Date(), ctx: ReasonContext = {}): ReasonPart[] {
  const when = (at?: string) => (at ? ` · ${formatRelativeDays(at, now)}` : '');
  switch (r.kind) {
    case 'in_progress': {
      const pct = Math.round(Math.min(1, Math.max(0, r.fraction)) * 100);
      // Неразрывный пробел: «42 %» не переносится по «%».
      const left = r.minutes ? ` · ещё ${formatReadingTime(r.minutes)}` : '';
      return [{ text: `Остановились на ${pct}\u00a0%${when(r.at)}${left}` }];
    }
    case 'new_by_author':
      return [
        { text: 'По подписке на автора: ' },
        { text: r.author, link: { to: '/authors/$id', id: r.author_id } },
        ...(r.at ? [{ text: ` · добавлена ${formatRelativeDays(r.at, now)}` }] : []),
      ];
    case 'new_in_series':
      return [
        { text: 'По подписке на цикл: ' },
        { text: r.series, link: { to: '/series/$id', id: r.series_id } },
        ...(r.at ? [{ text: ` · добавлена ${formatRelativeDays(r.at, now)}` }] : []),
      ];
    case 'acquired':
      return [{ text: r.at ? `Скачали ${formatRelativeDays(r.at, now)}` : 'Скачали' }];
    case 'finished':
      return [{ text: r.at ? `Дочитали ${formatRelativeDays(r.at, now)}` : 'Дочитали' }];
    case 'searched_appeared':
      return [{ text: `Вы искали «${r.query}»${when(r.at)}` }];
    case 'next_in_series':
      // «Дочитали «Том 2» · № 3 в цикле Ведьмак» — основание и место в цикле.
      return [
        { text: `Дочитали «${r.title}» · ${r.ser_no ? `№ ${r.ser_no} в цикле ` : 'дальше в цикле '}` },
        { text: r.series, link: { to: '/series/$id', id: r.series_id } },
      ];
    case 'author_you_finished':
      return [
        { text: `Дочитали «${r.title}» · автор: ` },
        { text: r.author, link: { to: '/authors/$id', id: r.author_id } },
      ];
    case 'dropped_then_loved_author':
      // «Отложили год назад · с тех пор 2 книги автора: X».
      return [
        {
          text: `Отложили${r.at ? ` ${formatRelativeDays(r.at, now)}` : ''} · с тех пор ${r.count} ${pluralBooksOf(r.count)} автора: `,
        },
        { text: r.author, link: { to: '/authors/$id', id: r.author_id } },
      ];
    case 'reread':
      return [{ text: `Поставили 5${r.at ? ` · читали ${formatRelativeDays(r.at, now)}` : ''} · перечитать?` }];
    case 'forgotten_classic': {
      const rating =
        r.source === 'fantlab' && r.rating
          ? `Фантлаб ${r.rating.toFixed(1)}`
          : r.source === 'librate' && r.rating
            ? `рейтинг ${r.rating.toFixed(0)}/5`
            : '';
      return [
        {
          text: [r.year ? `${r.year} год` : 'Классика', rating, 'здесь ещё никто не читал'].filter(Boolean).join(' · '),
        },
      ];
    }
    case 'fits_tonight': {
      // «≈ 40 мин · ваш жанр: Фантастика» — имя жанра по коду даёт компонент.
      const genre = r.genre ? (ctx.genreName?.(r.genre) ?? r.genre) : '';
      return [
        {
          text: [r.minutes ? formatReadingTime(r.minutes) : '', genre ? `ваш жанр: ${genre}` : '']
            .filter(Boolean)
            .join(' · '),
        },
      ];
    }
    default:
      return [];
  }
}

/** reasonText — та же причина одной строкой (aria-label, тесты). */
export function reasonText(r: RecReason, now = new Date(), ctx: ReasonContext = {}): string {
  return reasonParts(r, now, ctx)
    .map((p) => p.text)
    .join('');
}
