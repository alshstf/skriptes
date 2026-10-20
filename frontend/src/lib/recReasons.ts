import type { RecReason } from './recs';
import { formatReadingTime, formatRelativeDays } from './format';

/**
 * recReasons.ts — текст причины рекомендации по её виду (контракт recsys.Reason).
 * Чистые функции без React: компонент RecReasons (components/RecReason.tsx)
 * превращает части в строку со ссылками. Обращение — на «вы», как везде в UI.
 */

// ReasonPart — кусок текста причины; link — ссылка на автора/цикл.
export type ReasonPart = { text: string; link?: { to: '/authors/$id' | '/series/$id'; id: number } };

/** reasonParts — части текста причины; пустой массив — причину не показываем. */
export function reasonParts(r: RecReason, now = new Date()): ReasonPart[] {
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
    default:
      return [];
  }
}

/** reasonText — та же причина одной строкой (aria-label, тесты). */
export function reasonText(r: RecReason, now = new Date()): string {
  return reasonParts(r, now)
    .map((p) => p.text)
    .join('');
}
