import { useQuery } from '@tanstack/react-query';
import { apiFetch } from './api';

export type Adaptation = {
  id: number;
  provider: string;
  ext_id: string;
  title: string;
  year?: number;
  director?: string;
  kind: string; // "film" | "tv_series" | "miniseries" | "anime" | "other"
  poster_path?: string;
  ext_url?: string;
};

export type AdaptationsResponse = {
  items: Adaptation[];
  enrichment_status: 'pending' | 'done';
};

/**
 * useAdaptations — список экранизаций книги.
 *
 * Поллинг до enrichment_status === "done": фоновое обогащение через
 * Wikidata SPARQL может занять до 10-15 секунд (тяжелее чем cover/annotation).
 * Сдаёмся после ~30 секунд (15 попыток по 2с), чтобы не молотить запросы
 * для книг где enrichment по какой-то причине застрял.
 *
 * После status==="done" не ретраимся: бэкенд пометил adaptations_fetched_at,
 * результат финальный (пустой items === "ничего не сняли по этой книге").
 *
 * placeholderData намеренно не ставим — если ничего нет, секция вообще
 * не рендерится (см. BookDetailPage).
 */
const ADAPT_MAX_TRIES = 15;

export function useAdaptations(bookId: number | string | undefined) {
  const id = bookId === undefined || bookId === '' ? undefined : String(bookId);
  return useQuery<AdaptationsResponse>({
    queryKey: ['book-adaptations', id],
    queryFn: ({ signal }) =>
      apiFetch<AdaptationsResponse>(`/api/books/${id}/adaptations`, { signal }),
    enabled: id !== undefined,
    refetchInterval: (q) => {
      const data = q.state.data;
      if (!data) return 2_000;
      if (data.enrichment_status === 'done') return false;
      if (q.state.dataUpdateCount > ADAPT_MAX_TRIES) return false;
      return 2_000;
    },
  });
}
