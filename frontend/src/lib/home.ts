import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from './api';

/**
 * home.ts — данные для новой Главной (`/`).
 *
 * Два динамических блока:
 *  - useContinueReading() — книги «в процессе» (есть прогресс, не дочитаны),
 *    GET /api/me/continue-reading;
 *  - useSubscriptionFeed() — свежие книги авторов из подписок,
 *    GET /api/me/feed/subscriptions.
 *
 * Оба возвращают компактные DTO, которых хватает на карточку-обложку: id
 * издания (для on-demand-обложки и ссылки-фолбэка), work_id (ссылка на
 * карточку книги), название, авторы, серия и обложка. У «продолжить чтение»
 * дополнительно fraction прогресса.
 */

// ContinueItem — зеркало history.ContinueItem (backend).
export type ContinueItem = {
  id: number;
  work_id?: number;
  title: string;
  authors: string[];
  series?: string;
  lib_id: string;
  cover_path?: string;
  fraction: number;
  updated_at: string;
};

// FeedItem — зеркало history.FeedItem (backend).
export type FeedItem = {
  id: number;
  work_id?: number;
  title: string;
  authors: string[];
  series?: string;
  lib_id: string;
  cover_path?: string;
  added_at?: string;
};

// RateableItem — зеркало history.RateableItem (книга в блоке «Оцените прочитанное»).
export type RateableItem = {
  id: number;
  work_id?: number;
  title: string;
  authors: string[];
  series?: string;
  lib_id: string;
  cover_path?: string;
};

/** queryKey ленты запросов оценки (мутации в lib/ratings.ts инвалидируют по префиксу). */
export const RATEABLE_FEED_KEY = ['me', 'rating-prompts', 'feed'] as const;

type ItemsResponse<T> = { items: T[] };

/**
 * useContinueReading — книги «в процессе» для блока «Продолжить чтение».
 * staleTime скромный: прогресс мог обновиться в ридере, хотим свежий список
 * при возврате на Главную.
 */
export function useContinueReading(limit = 12) {
  return useQuery<ContinueItem[]>({
    queryKey: ['me', 'continue-reading', limit],
    queryFn: async ({ signal }) => {
      const r = await apiFetch<ItemsResponse<ContinueItem>>(
        `/api/me/continue-reading?limit=${limit}`,
        { signal },
      );
      return r.items;
    },
    staleTime: 10_000,
  });
}

/**
 * useSubscriptionFeed — свежие книги подписанных авторов. Меняется редко
 * (только при импорте новых книг) — держим длиннее в кэше.
 */
export function useSubscriptionFeed(limit = 12) {
  return useQuery<FeedItem[]>({
    queryKey: ['me', 'feed', 'subscriptions', limit],
    queryFn: async ({ signal }) => {
      const r = await apiFetch<ItemsResponse<FeedItem>>(
        `/api/me/feed/subscriptions?limit=${limit}`,
        { signal },
      );
      return r.items;
    },
    staleTime: 60_000,
  });
}

/**
 * useRateablePrompts — книги, которые юзер вероятно прочитал и ещё не оценил
 * (блок «Оцените прочитанное»). Backend сам возвращает пусто, если запросы
 * оценки выключены в профиле → блок прячется. Меняется при оценке/скрытии —
 * скромный staleTime + инвалидация из мутаций (lib/ratings.ts).
 */
export function useRateablePrompts(limit = 12) {
  return useQuery<RateableItem[]>({
    queryKey: [...RATEABLE_FEED_KEY, limit],
    queryFn: async ({ signal }) => {
      const r = await apiFetch<ItemsResponse<RateableItem>>(
        `/api/me/rating-prompts/feed?limit=${limit}`,
        { signal },
      );
      return r.items;
    },
    staleTime: 30_000,
  });
}

/**
 * useDismissFeedItem — скрыть работу из ленты «Новинки по подпискам»
 * («не интересно»). Оптимистично убираем работу из всех закэшированных
 * страниц ленты, чтобы карточка исчезла мгновенно; на ошибке — откат.
 * Скрытие персистентно (backend feed_dismissals), книга не вернётся.
 */
export function useDismissFeedItem() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (workId: number) => {
      await apiFetch<void>('/api/me/feed/dismiss', { method: 'POST', body: { work_id: workId } });
      return workId;
    },
    onMutate: async (workId) => {
      await qc.cancelQueries({ queryKey: ['me', 'feed', 'subscriptions'] });
      const prev = qc.getQueriesData<FeedItem[]>({ queryKey: ['me', 'feed', 'subscriptions'] });
      for (const [key, data] of prev) {
        if (data) {
          qc.setQueryData(
            key,
            data.filter((it) => (it.work_id ?? it.id) !== workId),
          );
        }
      }
      return { prev };
    },
    onError: (_e, _workId, ctx) => {
      ctx?.prev?.forEach(([key, data]) => qc.setQueryData(key, data));
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: ['me', 'feed', 'subscriptions'] });
    },
  });
}
