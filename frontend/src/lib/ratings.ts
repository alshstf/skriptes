/**
 * lib/ratings.ts — пользовательская оценка книги (work-level, шкала 1–5).
 * Отдельно от библиотечного рейтинга (books.rating / LIBRATE).
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { apiFetch } from './api';
import type { Book } from './books';
import { RATEABLE_FEED_KEY } from './home';

/**
 * useRateBook — поставить/снять оценку работе (1–5; null = снять).
 * cardKey — queryKey открытой карточки (['book', id] или ['work', id]):
 * оптимистично патчим user_rating в её кэше, на settled инвалидируем, чтобы
 * подтянуть свежие rating_avg / rating_count.
 */
export function useRateBook(
  workId: number | undefined,
  cardKey: readonly (string | number)[],
) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (rating: number | null) => {
      if (workId == null) throw new Error('no work id');
      if (rating == null) {
        await apiFetch(`/api/works/${workId}/rating`, { method: 'DELETE' });
      } else {
        await apiFetch(`/api/works/${workId}/rating`, { method: 'PUT', body: { rating } });
      }
      return rating;
    },
    onMutate: async (rating) => {
      await qc.cancelQueries({ queryKey: cardKey });
      const prev = qc.getQueryData<Book>([...cardKey]);
      if (prev) {
        qc.setQueryData<Book>([...cardKey], { ...prev, user_rating: rating ?? undefined });
      }
      return { prev };
    },
    onError: (_e, _v, ctx) => {
      if (ctx?.prev) qc.setQueryData([...cardKey], ctx.prev);
      toast.error('Не удалось сохранить оценку');
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: cardKey });
    },
  });
}

// ── Отложенные запросы оценки («Оцените прочитанное») ──────────────

export type RatingPromptSettings = { enabled: boolean; delay_days: number };

/**
 * useRatePrompt — оценка книги из блока «Оцените прочитанное» на Главной.
 * Успех → книга уходит из ленты (инвалидация) + обновляем карточку. Бэкенд
 * заодно авто-проставляет «Прочитана».
 */
export function useRatePrompt() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ workId, rating }: { workId: number; rating: number }) =>
      apiFetch(`/api/works/${workId}/rating`, { method: 'PUT', body: { rating } }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: RATEABLE_FEED_KEY });
      void qc.invalidateQueries({ queryKey: ['work'] });
      void qc.invalidateQueries({ queryKey: ['book'] });
      void qc.invalidateQueries({ queryKey: ['me', 'favorites'] });
      toast.success('Оценка сохранена');
    },
    onError: () => toast.error('Не удалось сохранить оценку'),
  });
}

/** useDismissRatingPrompt — «Не буду оценивать»: скрыть до явного прочтения. */
export function useDismissRatingPrompt() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (workId: number) =>
      apiFetch(`/api/works/${workId}/rating-prompt/dismiss`, { method: 'POST' }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: RATEABLE_FEED_KEY });
      toast.success('Убрали из запросов на оценку');
    },
    onError: () => toast.error('Не удалось скрыть'),
  });
}

/** useSnoozeRatingPrompt — «Ещё не прочитал»: спросить позже. */
export function useSnoozeRatingPrompt() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (workId: number) =>
      apiFetch(`/api/works/${workId}/rating-prompt/snooze`, { method: 'POST' }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: RATEABLE_FEED_KEY });
      toast.success('Спросим позже');
    },
    onError: () => toast.error('Не удалось отложить'),
  });
}

/** useRatingPromptSettings — настройки запросов оценки (профиль). */
export function useRatingPromptSettings() {
  return useQuery<RatingPromptSettings>({
    queryKey: ['me', 'rating-prompts'],
    queryFn: ({ signal }) => apiFetch<RatingPromptSettings>('/api/me/rating-prompts', { signal }),
    staleTime: 60_000,
  });
}

export function useUpdateRatingPromptSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (cfg: RatingPromptSettings) =>
      apiFetch<RatingPromptSettings>('/api/me/rating-prompts', { method: 'PUT', body: cfg }),
    onSuccess: (saved) => {
      qc.setQueryData(['me', 'rating-prompts'], saved);
      // Вкл/выкл и задержка меняют состав ленты «Оцените прочитанное».
      void qc.invalidateQueries({ queryKey: RATEABLE_FEED_KEY });
    },
    onError: () => toast.error('Не удалось сохранить настройку'),
  });
}
