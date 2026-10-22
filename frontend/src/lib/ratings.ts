/**
 * lib/ratings.ts — пользовательская оценка книги (work-level, шкала 1–5).
 * Отдельно от библиотечного рейтинга (books.rating / LIBRATE).
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { apiFetch } from './api';
import type { Book } from './books';
import { RATEABLE_FEED_KEY, RECS_KEY } from './recs';

/** RatingReason — причина низкой оценки (план recsys/03, 3.1); закрытый список — recsys.AllRatingReasons. */
export type RatingReason = 'too_long' | 'gloomy' | 'language' | 'not_mine' | 'unfinished' | 'dated' | 'boring';

/** RATING_REASONS — чипы второго шага «почему?» в порядке показа. */
export const RATING_REASONS: readonly { key: RatingReason; label: string }[] = [
  { key: 'too_long', label: 'слишком длинно' },
  { key: 'gloomy', label: 'мрачно' },
  { key: 'language', label: 'язык / перевод' },
  { key: 'not_mine', label: 'не моё' },
  { key: 'unfinished', label: 'не дочитал' },
  { key: 'dated', label: 'устарело' },
  { key: 'boring', label: 'скучно' },
];

/**
 * Оценка ≤ LOW_RATING_MAX спрашивает «почему?» сразу (второй шаг, можно
 * пропустить); ≤ REASONS_OPTIONAL_MAX — по желанию (ссылка «почему?»). Выше —
 * причин нет (бэк снимает их при оценке ≥ 4).
 */
export const LOW_RATING_MAX = 2;
export const REASONS_OPTIONAL_MAX = 3;

export function reasonLabel(key: RatingReason): string {
  return RATING_REASONS.find((r) => r.key === key)?.label ?? key;
}

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
        // Причины живут с низкой оценкой: снятие или ≥ 4 их убирает (как бэк).
        const keepReasons = rating != null && rating <= REASONS_OPTIONAL_MAX;
        qc.setQueryData<Book>([...cardKey], {
          ...prev,
          user_rating: rating ?? undefined,
          user_rating_reasons: keepReasons ? prev.user_rating_reasons : undefined,
        });
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
 * заодно авто-проставляет «Прочитана». Низкая оценка (≤ LOW_RATING_MAX)
 * ленту не инвалидирует: карточка остаётся на второй шаг «почему?»
 * (RatingReasonsPrompt), который зовёт useFinishRatePrompt.
 */
export function useRatePrompt() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ workId, rating }: { workId: number; rating: number }) =>
      apiFetch(`/api/works/${workId}/rating`, { method: 'PUT', body: { rating } }),
    onSuccess: (_data, vars) => {
      if (vars.rating > LOW_RATING_MAX) {
        void qc.invalidateQueries({ queryKey: RATEABLE_FEED_KEY });
      }
      void qc.invalidateQueries({ queryKey: ['work'] });
      void qc.invalidateQueries({ queryKey: ['book'] });
      void qc.invalidateQueries({ queryKey: ['me', 'favorites'] });
      toast.success('Оценка сохранена');
    },
    onError: () => toast.error('Не удалось сохранить оценку'),
  });
}

/** useFinishRatePrompt — после второго шага (причины или пропуск) убрать карточку из «Оцените прочитанное». */
export function useFinishRatePrompt() {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: RATEABLE_FEED_KEY });
  };
}

/**
 * useSetRatingReasons — причины низкой оценки (план recsys/03, 3.1): PUT
 * заменяет набор (пустой — убрать). cardKey — кэш открытой карточки (патчим
 * user_rating_reasons); лента инвалидируется — модель близости учла причину.
 */
export function useSetRatingReasons(workId: number | undefined, cardKey?: readonly (string | number)[]) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (reasons: RatingReason[]) => {
      if (workId == null) throw new Error('no work id');
      const r = await apiFetch<{ reasons: RatingReason[] }>(`/api/works/${workId}/rating-reasons`, {
        method: 'PUT',
        body: { reasons },
      });
      return r.reasons;
    },
    onSuccess: (saved) => {
      if (cardKey) {
        const prev = qc.getQueryData<Book>([...cardKey]);
        if (prev) {
          qc.setQueryData<Book>([...cardKey], {
            ...prev,
            user_rating_reasons: saved.length > 0 ? saved : undefined,
          });
        }
      }
      void qc.invalidateQueries({ queryKey: RECS_KEY });
    },
    onError: () => toast.error('Не удалось сохранить причину'),
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
