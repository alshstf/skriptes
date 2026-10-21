import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { apiFetch } from './api';
import type { RecSectionKind } from './recs';

/**
 * homeFeed.ts — персональные настройки ленты Главной (план recsys/02, 2.9):
 * свёрнутые секции. Хранятся на сервере (user_settings, /api/me/home-feed) —
 * синхронно между устройствами. Пока настройки не загрузились (или не
 * загрузились вовсе) — всё развёрнуто.
 */

export type HomeFeedSettings = { collapsed: RecSectionKind[] };

export const HOME_FEED_KEY = ['me', 'home-feed'] as const;

/** useHomeFeedSettings — свёрнутые секции; ошибка/загрузка = ничего не свёрнуто. */
export function useHomeFeedSettings() {
  return useQuery<HomeFeedSettings>({
    queryKey: HOME_FEED_KEY,
    queryFn: ({ signal }) => apiFetch<HomeFeedSettings>('/api/me/home-feed', { signal }),
    staleTime: 5 * 60_000,
    retry: false,
  });
}

/**
 * useSetSectionCollapsed — свернуть/развернуть секцию: оптимистично правим
 * кэш, PUT шлёт весь список; на ошибке — откат и тост.
 */
export function useSetSectionCollapsed() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ kind, collapsed }: { kind: RecSectionKind; collapsed: boolean }) => {
      const cur = qc.getQueryData<HomeFeedSettings>(HOME_FEED_KEY)?.collapsed ?? [];
      const next = collapsed ? Array.from(new Set([...cur, kind])) : cur.filter((k) => k !== kind);
      return apiFetch<HomeFeedSettings>('/api/me/home-feed', {
        method: 'PUT',
        body: { collapsed: next },
      });
    },
    onMutate: async ({ kind, collapsed }) => {
      await qc.cancelQueries({ queryKey: HOME_FEED_KEY });
      const prev = qc.getQueryData<HomeFeedSettings>(HOME_FEED_KEY);
      const cur = prev?.collapsed ?? [];
      qc.setQueryData<HomeFeedSettings>(HOME_FEED_KEY, {
        collapsed: collapsed ? Array.from(new Set([...cur, kind])) : cur.filter((k) => k !== kind),
      });
      return { prev };
    },
    onError: (_e, _v, ctx) => {
      qc.setQueryData(HOME_FEED_KEY, ctx?.prev);
      toast.error('Не удалось сохранить настройку ленты');
    },
    onSuccess: (saved) => {
      qc.setQueryData(HOME_FEED_KEY, saved);
    },
  });
}
