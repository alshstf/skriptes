import { useQuery } from '@tanstack/react-query';
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
