import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { apiFetch } from './api';
import { RECS_KEY } from './recs';

/**
 * blocklist.ts — «не предлагать» автора или цикл (план recsys/03, 3.2). Это не
 * скрытие контента: каталог и поиск не меняются, только лента рекомендаций и
 * новинки по подпискам. Кнопка — на карточке автора/цикла и в меню карточки
 * ленты; список с отменой — в профиле (BlocklistCard).
 */

export type BlockTarget = 'author' | 'series';
export type BlockedAuthor = { id: number; full_name: string; added_at: string };
export type BlockedSeries = { id: number; title: string; author_name?: string; added_at: string };
export type Blocklist = { authors: BlockedAuthor[]; series: BlockedSeries[] };

export const BLOCKLIST_KEY = ['me', 'blocklist'] as const;

const PATH: Record<BlockTarget, (id: number) => string> = {
  author: (id) => `/api/authors/${id}/block`,
  series: (id) => `/api/series/${id}/block`,
};

export function useBlocklist() {
  return useQuery<Blocklist>({
    queryKey: BLOCKLIST_KEY,
    queryFn: ({ signal }) => apiFetch<Blocklist>('/api/me/blocklist', { signal }),
    staleTime: 30_000,
  });
}

/**
 * useToggleBlock — POST/DELETE …/block. Оптимистично правим is_blocked в кэше
 * карточки (['author', id] / ['series', id] — те же ключи, что useAuthor/
 * useSeries), на settled обновляем список и ленту (карточки заблокированного
 * уходят из секций после refetch).
 */
export function useToggleBlock() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (vars: { target: BlockTarget; id: number; next: boolean }) => {
      await apiFetch(PATH[vars.target](vars.id), { method: vars.next ? 'POST' : 'DELETE' });
      return vars.next;
    },
    onMutate: async ({ target, id, next }) => {
      const key = [target, String(id)];
      await qc.cancelQueries({ queryKey: key });
      const prev = qc.getQueryData<Record<string, unknown>>(key);
      if (prev) {
        qc.setQueryData(key, { ...prev, is_blocked: next });
      }
      return { prev, key };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev && ctx.key) {
        qc.setQueryData(ctx.key, ctx.prev);
      }
      toast.error('Не удалось сохранить');
    },
    onSuccess: (_data, vars) => {
      const noun = vars.target === 'author' ? 'Автора' : 'Цикл';
      toast.success(vars.next ? `${noun} больше не предлагаем` : `${noun} снова предлагаем`);
    },
    onSettled: (_data, _err, _vars, ctx) => {
      if (ctx?.key) {
        void qc.invalidateQueries({ queryKey: ctx.key });
      }
      void qc.invalidateQueries({ queryKey: BLOCKLIST_KEY });
      void qc.invalidateQueries({ queryKey: RECS_KEY });
    },
  });
}
