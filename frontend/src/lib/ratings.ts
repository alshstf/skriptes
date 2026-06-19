/**
 * lib/ratings.ts — пользовательская оценка книги (work-level, шкала 1–5).
 * Отдельно от библиотечного рейтинга (books.rating / LIBRATE).
 */

import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { apiFetch } from './api';
import type { Book } from './books';

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
