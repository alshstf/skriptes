import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from './api';

/**
 * FavoriteTarget — что добавляется в избранное: книга, автор или серия.
 * Все три типа симметричны в UX и backend; разница только в URL и в
 * имени react-query-кэша, который нужно инвалидировать.
 */
export type FavoriteTarget = 'book' | 'author' | 'series';

const PATH: Record<FavoriteTarget, (id: number) => string> = {
  book: (id) => `/api/books/${id}/favorite`,
  author: (id) => `/api/authors/${id}/favorite`,
  series: (id) => `/api/series/${id}/favorite`,
};

// queryKey для текущей сущности — той, на чьей карточке стоит кнопка.
// Сохраняем тот же ключ, что используют useBook/useAuthor/useSeries.
const RESOURCE_KEY: Record<FavoriteTarget, string> = {
  book: 'book',
  author: 'author',
  series: 'series',
};

/**
 * useToggleFavorite — универсальная мутация: на каждый клик переключает
 * флаг на сервере и оптимистично обновляет кэш конкретной карточки.
 *
 * Принцип: cancelQueries → setQueryData → rollback на ошибку →
 * invalidate на успех (и заодно invalidate ['me','favorites'] чтобы
 * страница "Избранное" подхватила изменение).
 */
export function useToggleFavorite() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (vars: { target: FavoriteTarget; id: number; next: boolean }) => {
      await apiFetch(PATH[vars.target](vars.id), { method: vars.next ? 'POST' : 'DELETE' });
      return vars.next;
    },
    onMutate: async ({ target, id, next }) => {
      const key = [RESOURCE_KEY[target], String(id)];
      await qc.cancelQueries({ queryKey: key });
      const prev = qc.getQueryData<Record<string, unknown>>(key);
      if (prev) {
        qc.setQueryData(key, { ...prev, is_favorite: next });
      }
      return { prev, key };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev && ctx.key) {
        qc.setQueryData(ctx.key, ctx.prev);
      }
    },
    onSettled: (_data, _err, _vars, ctx) => {
      if (ctx?.key) {
        void qc.invalidateQueries({ queryKey: ctx.key });
      }
      void qc.invalidateQueries({ queryKey: ['me', 'favorites'] });
      // Меняем сигнал персонализации — перерисовываем кэш списков
      // и палитры поиска: без этого re-rank продолжит отдавать старый
      // порядок до истечения staleTime.
      void qc.invalidateQueries({ queryKey: ['suggest'] });
      void qc.invalidateQueries({ queryKey: ['books'] });
    },
  });
}

// ── /api/me/favorites ────────────────────────────────────────────

export type FavoriteBook = {
  id: number;
  title: string;
  authors: string[];
  series?: string;
  lang?: string;
  lib_id: string;
  added_at: string;
};

export type FavoriteAuthor = {
  id: number;
  full_name: string;
  book_count: number;
  added_at: string;
};

export type FavoriteSeriesItem = {
  id: number;
  title: string;
  author_name?: string;
  book_count: number;
  added_at: string;
};

export type AllFavorites = {
  books: FavoriteBook[];
  authors: FavoriteAuthor[];
  series: FavoriteSeriesItem[];
};

export function useMyFavorites() {
  return useQuery<AllFavorites>({
    queryKey: ['me', 'favorites'],
    queryFn: ({ signal }) => apiFetch<AllFavorites>('/api/me/favorites', { signal }),
    staleTime: 30_000,
  });
}
