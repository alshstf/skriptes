/**
 * lib/content.ts — клиентские хуки раздела «Контент» (видимость книг по
 * языкам/жанрам).
 *
 * Три потребителя:
 *  - панель фильтров /books — useEffectiveContent (admin ∪ персональные
 *    скрытые), чтобы не показывать скрытые опции;
 *  - админка /admin/content — useAdminContent (глобально для всех);
 *  - профиль /me/content — useMyContent (персонально, admin-скрытые
 *    приходят как read-only locked).
 *
 * useLanguages — полный список языков коллекции (для обоих разделов
 * «Контент»; НЕ фильтруется по скрытым — их как раз надо показать).
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from './api';

export type LanguageItem = {
  code: string;
  display: string;
  book_count: number;
};

export type ContentSettings = {
  hidden_genres: string[];
  hidden_languages: string[];
};

export type MyContentSettings = ContentSettings & {
  admin_hidden_genres: string[];
  admin_hidden_languages: string[];
};

type LanguagesResponse = { items: LanguageItem[] };

/** sameSet — сравнение двух наборов кодов без учёта порядка (для dirty-флага). */
export function sameSet(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false;
  const set = new Set(a);
  return b.every((x) => set.has(x));
}

/**
 * useLanguages — все языки коллекции с числом книг (для разделов
 * «Контент»). staleTime 5 минут: множество языков меняется только при
 * импорте новой коллекции.
 */
export function useLanguages() {
  return useQuery<LanguageItem[]>({
    queryKey: ['languages'],
    queryFn: async () => {
      const r = await apiFetch<LanguagesResponse>('/api/languages');
      return r.items;
    },
    staleTime: 5 * 60_000,
  });
}

/**
 * useEffectiveContent — объединённый набор скрытого (admin ∪ персональное)
 * для текущего пользователя. Панель фильтров прячет эти жанры/языки.
 * Бэкенд уже исключает их из выдачи — это только для чистого UI.
 */
export function useEffectiveContent() {
  return useQuery<ContentSettings>({
    queryKey: ['content', 'effective'],
    queryFn: () => apiFetch<ContentSettings>('/api/content/effective'),
    staleTime: 60_000,
  });
}

const ADMIN_KEY = ['admin', 'content'] as const;

export function useAdminContent() {
  return useQuery<ContentSettings>({
    queryKey: [...ADMIN_KEY],
    queryFn: () => apiFetch<ContentSettings>('/api/admin/content'),
    staleTime: 30_000,
  });
}

export function useUpdateAdminContent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: ContentSettings) =>
      apiFetch<ContentSettings>('/api/admin/content', { method: 'PUT', body: vars }),
    onSuccess: (data) => {
      qc.setQueryData([...ADMIN_KEY], data);
      // Глобальные изменения влияют на выдачу всех пользователей.
      qc.invalidateQueries({ queryKey: ['content', 'effective'] });
    },
  });
}

const ME_KEY = ['me', 'content'] as const;

export function useMyContent() {
  return useQuery<MyContentSettings>({
    queryKey: [...ME_KEY],
    queryFn: () => apiFetch<MyContentSettings>('/api/me/content'),
    staleTime: 30_000,
  });
}

export function useUpdateMyContent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: ContentSettings) =>
      apiFetch<MyContentSettings>('/api/me/content', { method: 'PUT', body: vars }),
    onSuccess: (data) => {
      qc.setQueryData([...ME_KEY], data);
      qc.invalidateQueries({ queryKey: ['content', 'effective'] });
    },
  });
}
