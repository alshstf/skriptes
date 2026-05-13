import { useMutation, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from './api';
import type { Book } from './books';

/**
 * useToggleFavorite — мутация для звёздочки на BookDetailPage.
 *
 * Оптимистично переключаем флаг в ['book', id] кэше; на rollback
 * возвращаем прошлое значение. /api/me/favorites инвалидируем после
 * успеха, чтобы страница "Избранное" подхватила (когда такая появится).
 */
export function useToggleFavorite() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (vars: { bookId: number; next: boolean }) => {
      const path = `/api/books/${vars.bookId}/favorite`;
      await apiFetch(path, { method: vars.next ? 'POST' : 'DELETE' });
      return vars.next;
    },
    onMutate: async ({ bookId, next }) => {
      await qc.cancelQueries({ queryKey: ['book', String(bookId)] });
      const prev = qc.getQueryData<Book & { is_favorite?: boolean }>([
        'book',
        String(bookId),
      ]);
      if (prev) {
        qc.setQueryData(['book', String(bookId)], { ...prev, is_favorite: next });
      }
      return { prev };
    },
    onError: (_err, { bookId }, ctx) => {
      if (ctx?.prev) {
        qc.setQueryData(['book', String(bookId)], ctx.prev);
      }
    },
    onSettled: (_data, _err, { bookId }) => {
      void qc.invalidateQueries({ queryKey: ['book', String(bookId)] });
      void qc.invalidateQueries({ queryKey: ['me', 'favorites'] });
    },
  });
}
