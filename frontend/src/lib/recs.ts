import { queryOptions, useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from './api';

/**
 * recs.ts — лента рекомендаций на Главной (план recsys/01, задача 1.3).
 *
 * Один контракт для всех секций: GET /api/me/recommendations?section=<kind>
 * → { section, items: RecItem[] }, у каждой карточки — причины (RecReason),
 * текст которых собирает фронт (lib/recReasons.ts). Секции на главной
 * запрашиваются по отдельности (свой queryKey, свой staleTime), порядок —
 * в pages/HomePage.tsx. Новая секция = новый RecSectionKind здесь +
 * провайдер на бэке (api/recs.go; секции правил фазы 2 — recsys.Rules).
 */

export type RecSectionKind =
  | 'continue_reading'
  | 'subscriptions'
  | 'rate_prompts'
  | 'searched_appeared'
  | 'next_in_series'
  | 'author_you_finished';

/**
 * HOME_SECTIONS — порядок секций на Главной (план recsys/02, 2.9): продолжить →
 * появилось искомое → дальше в цикле → автор, которого дочитали → новинки по
 * подпискам → оцените прочитанное. Пустые не рендерятся.
 */
export const HOME_SECTIONS: readonly RecSectionKind[] = [
  'continue_reading',
  'searched_appeared',
  'next_in_series',
  'author_you_finished',
  'subscriptions',
  'rate_prompts',
];

// RecReason — зеркало recsys.Reason (backend): дискриминант kind, у каждого
// вида — свои заполненные поля.
export type RecReason =
  | { kind: 'in_progress'; fraction: number; at?: string; minutes?: number }
  | { kind: 'new_by_author'; author_id: number; author: string; at?: string }
  | { kind: 'new_in_series'; series_id: number; series: string; at?: string }
  | { kind: 'acquired'; at?: string }
  | { kind: 'finished'; at?: string }
  | { kind: 'searched_appeared'; query: string; at?: string }
  // Правила фазы 2: work_id/title — прочитанная работа-основание.
  | { kind: 'next_in_series'; series_id: number; series: string; work_id: number; title: string; ser_no?: number; at?: string }
  | { kind: 'author_you_finished'; author_id: number; author: string; work_id: number; title: string; at?: string };

// RecItem — зеркало recsys.Item: id — представительное издание (обложка,
// фолбэк ссылки), work_id — карточка книги.
export type RecItem = {
  id: number;
  work_id?: number;
  title: string;
  authors: string[];
  series?: string;
  lib_id: string;
  cover_path?: string;
  reasons: RecReason[];
};

export type RecSection = { section: RecSectionKind; items: RecItem[] };

export type DismissReason = 'not_interested' | 'already_read' | 'later';

/** Префикс queryKey всех секций ленты (инвалидация и оптимистичное скрытие). */
export const RECS_KEY = ['me', 'recs'] as const;

/** recSectionKey — queryKey секции без лимита (инвалидировать по префиксу). */
export function recSectionKey(kind: RecSectionKind) {
  return [...RECS_KEY, kind] as const;
}

/** queryKey секции «Оцените прочитанное» (мутации в lib/ratings.ts инвалидируют по префиксу). */
export const RATEABLE_FEED_KEY = recSectionKey('rate_prompts');

// staleTime по секциям: прогресс чтения меняется в ридере — хотим свежий
// список при возврате; новинки — только при импорте; запросы оценки —
// инвалидируются мутациями (lib/ratings.ts).
const STALE_MS: Record<RecSectionKind, number> = {
  continue_reading: 10_000,
  subscriptions: 60_000,
  rate_prompts: 30_000,
  searched_appeared: 60_000,
  next_in_series: 60_000,
  author_you_finished: 60_000,
};

/** recSectionQueryOptions — запрос одной секции (для useRecSection и useQueries). */
export function recSectionQueryOptions(kind: RecSectionKind, limit = 12) {
  return queryOptions<RecItem[]>({
    queryKey: [...recSectionKey(kind), limit],
    queryFn: async ({ signal }) => {
      const r = await apiFetch<RecSection>(
        `/api/me/recommendations?section=${kind}&limit=${limit}`,
        { signal },
      );
      return r.items;
    },
    staleTime: STALE_MS[kind],
  });
}

/** useRecSection — карточки одной секции ленты. */
export function useRecSection(kind: RecSectionKind, limit = 12) {
  return useQuery(recSectionQueryOptions(kind, limit));
}

/**
 * useHomeSections — все секции Главной разом (порядок HOME_SECTIONS), чтобы
 * страница знала, когда лента пуста целиком (пустое состояние нового
 * пользователя), а не только по секции.
 */
export function useHomeSections(limit = 12) {
  return useQueries({
    queries: HOME_SECTIONS.map((kind) => recSectionQueryOptions(kind, limit)),
    combine: (results) => ({
      sections: HOME_SECTIONS.map((kind, i) => ({
        kind,
        items: results[i].data ?? [],
        isLoading: results[i].isLoading,
      })),
      allLoaded: results.every((r) => !r.isLoading),
      allEmpty: results.every((r) => !r.isLoading && (r.data?.length ?? 0) === 0),
    }),
  });
}

/** workIdOf — id работы карточки (фолбэк — издание, если работы нет). */
export function workIdOf(it: Pick<RecItem, 'id' | 'work_id'>): number {
  return it.work_id ?? it.id;
}

/**
 * useDismissRec — скрыть работу из ленты с причиной («не интересно» /
 * «уже читал» / «потом»). Оптимистично убираем работу из ВСЕХ закэшированных
 * секций (скрытие — по работе, она могла лежать в нескольких); на ошибке —
 * откат. Скрытие персистентно (backend feed_dismissals), книга не вернётся.
 */
export function useDismissRec() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ workId, reason }: { workId: number; reason: DismissReason }) => {
      await apiFetch<void>('/api/me/feed/dismiss', {
        method: 'POST',
        body: { work_id: workId, reason },
      });
      return workId;
    },
    onMutate: async ({ workId }) => {
      await qc.cancelQueries({ queryKey: RECS_KEY });
      const prev = qc.getQueriesData<RecItem[]>({ queryKey: RECS_KEY });
      for (const [key, data] of prev) {
        if (data) {
          qc.setQueryData(
            key,
            data.filter((it) => workIdOf(it) !== workId),
          );
        }
      }
      return { prev };
    },
    onError: (_e, _v, ctx) => {
      ctx?.prev?.forEach(([key, data]) => qc.setQueryData(key, data));
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: RECS_KEY });
    },
  });
}
