import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { apiFetch } from './api';
import { RECS_KEY } from './recs';

/**
 * toRead.ts — полка «Хочу прочитать» (план recsys/03, 3.3): служебная полка
 * kind='to_read', закладка на карточке книги. Отличать от ★: избранное —
 * «люблю», «Хочу прочитать» — «ещё не читал». Как ★ — уровня работы (любое
 * издание на полке ⇒ книга на полке); Главная напоминает через 60 дней.
 */
export function useToggleToRead() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (vars: { bookId: number; next: boolean }) => {
      await apiFetch(`/api/books/${vars.bookId}/to-read`, { method: vars.next ? 'POST' : 'DELETE' });
      return vars.next;
    },
    onMutate: async ({ bookId, next }) => {
      const key = ['book', String(bookId)];
      await qc.cancelQueries({ queryKey: key });
      const prev = qc.getQueryData<Record<string, unknown>>(key);
      if (prev) {
        qc.setQueryData(key, { ...prev, is_to_read: next });
      }
      return { prev, key };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev && ctx.key) {
        qc.setQueryData(ctx.key, ctx.prev);
      }
      toast.error('Не удалось сохранить');
    },
    onSettled: (_data, _err, _vars, ctx) => {
      if (ctx?.key) {
        void qc.invalidateQueries({ queryKey: ctx.key });
      }
      // Карточка работы кэшируется под ['work', …]; полки — служебная полка в списке.
      void qc.invalidateQueries({ queryKey: ['work'] });
      void qc.invalidateQueries({ queryKey: ['me', 'collections'] });
      void qc.invalidateQueries({ queryKey: RECS_KEY });
    },
  });
}
