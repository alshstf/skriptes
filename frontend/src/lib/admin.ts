/**
 * lib/admin.ts — клиентские хуки для /api/admin/users.
 *
 * Используются только в AdminUsersPage. Все ответы 403 (от middleware
 * requireAdmin) пробрасываются как ApiError — page-level guard через
 * TanStack Router beforeLoad гарантирует что обычный юзер сюда вообще
 * не попадает, но если backend поменяет mind — фронт честно покажет
 * ошибку, а не молча провалится.
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from './api';
import type { Role } from './auth';

export type AdminUser = {
  id: number;
  email: string;
  display_name: string;
  role: Role;
  created_at: string;
};

type ListResponse = { items: AdminUser[] };

const KEY = ['admin', 'users'] as const;

/**
 * useAdminUsers — список всех пользователей для admin-UI.
 * Сортировка по created_at (от старых к новым) делается на бэке.
 */
export function useAdminUsers() {
  return useQuery<AdminUser[]>({
    queryKey: [...KEY],
    queryFn: async () => {
      const r = await apiFetch<ListResponse>('/api/admin/users');
      return r.items;
    },
    staleTime: 30_000,
  });
}

export function useCreateAdminUser() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: {
      email: string;
      display_name?: string;
      password: string;
      role: Role;
    }) => apiFetch<AdminUser>('/api/admin/users', { method: 'POST', body: vars }),
    onSuccess: () => qc.invalidateQueries({ queryKey: [...KEY] }),
  });
}

export function useUpdateAdminUser() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: {
      id: number;
      email?: string;
      display_name?: string;
      role?: Role;
    }) => {
      const { id, ...body } = vars;
      return apiFetch<AdminUser>(`/api/admin/users/${id}`, { method: 'PATCH', body });
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: [...KEY] }),
  });
}

/**
 * useResetAdminUserPassword — admin задаёт юзеру новый пароль без
 * верификации старого. Все сессии этого юзера ревоукаются на backend'е.
 */
export function useResetAdminUserPassword() {
  return useMutation({
    mutationFn: (vars: { id: number; new_password: string }) =>
      apiFetch<void>(`/api/admin/users/${vars.id}/password`, {
        method: 'PATCH',
        body: { new_password: vars.new_password },
      }),
  });
}

export function useDeleteAdminUser() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) =>
      apiFetch<void>(`/api/admin/users/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: [...KEY] }),
  });
}

// ── Кэш обложек ───────────────────────────────────────────────────

export type CoverCacheSettings = {
  cache_max_mb: number;
  cache_min_free_mb: number;
  prewarm: boolean;
  // статус прогрева (read-only): идёт ли прогон и какой
  prewarm_running: boolean;
  prewarm_mode: 'off' | 'continuous' | 'once';
  // статистика (read-only)
  cache_size_bytes: number;
  free_bytes: number;
};

const COVER_KEY = ['admin', 'cover-cache'] as const;

export function useCoverCacheSettings() {
  return useQuery<CoverCacheSettings>({
    queryKey: [...COVER_KEY],
    queryFn: () => apiFetch<CoverCacheSettings>('/api/admin/cover-cache'),
    staleTime: 10_000,
    // Пока прогрев идёт — поллим, чтобы видеть рост кэша и момент
    // завершения (тогда poll сам остановится).
    refetchInterval: (query) => (query.state.data?.prewarm_running ? 2000 : false),
  });
}

export function useUpdateCoverCacheSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { cache_max_mb: number; cache_min_free_mb: number; prewarm: boolean }) =>
      apiFetch<CoverCacheSettings>('/api/admin/cover-cache', { method: 'PUT', body: vars }),
    onSuccess: (data) => qc.setQueryData([...COVER_KEY], data),
  });
}

export function useClearCoverCache() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiFetch<{ removed: number }>('/api/admin/cover-cache/clear', { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: [...COVER_KEY] }),
  });
}

// usePrewarmCoverCache — разовый прогон прогрева (кнопка «Прогреть
// сейчас»). Запускается в фоне на бэке, отвечает сразу.
export function usePrewarmCoverCache() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiFetch<{ status: string }>('/api/admin/cover-cache/prewarm', { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: [...COVER_KEY] }),
  });
}

// useStopPrewarmCoverCache — остановить идущий разовый прогон.
export function useStopPrewarmCoverCache() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiFetch<{ status: string }>('/api/admin/cover-cache/prewarm/stop', { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: [...COVER_KEY] }),
  });
}

// ── Год издания: дозаполнение written_year из внешних источников ──────

export type YearEnrichmentSettings = {
  enabled: boolean;
  openlibrary: boolean;
  wikidata: boolean;
  openlibrary_rpm: number;
  wikidata_rpm: number;
  not_found_retry_days: number;
  error_retry_hours: number;
  // статус воркера (read-only)
  year_backfill_running: boolean;
  year_backfill_mode: 'off' | 'continuous' | 'once';
  // покрытие written_year (read-only)
  coverage: {
    total: number;
    with_year: number;
    by_source: Record<string, number>;
  };
};

// YearEnrichmentInput — тело PUT (только конфиг, без read-only полей).
export type YearEnrichmentInput = {
  enabled: boolean;
  openlibrary: boolean;
  wikidata: boolean;
  openlibrary_rpm: number;
  wikidata_rpm: number;
  not_found_retry_days: number;
  error_retry_hours: number;
};

const YEAR_KEY = ['admin', 'year-enrichment'] as const;

export function useYearEnrichmentSettings() {
  return useQuery<YearEnrichmentSettings>({
    queryKey: [...YEAR_KEY],
    queryFn: () => apiFetch<YearEnrichmentSettings>('/api/admin/year-enrichment'),
    staleTime: 10_000,
    // Пока воркер идёт — поллим, чтобы видеть рост покрытия и завершение.
    refetchInterval: (query) => (query.state.data?.year_backfill_running ? 3000 : false),
  });
}

export function useUpdateYearEnrichmentSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: YearEnrichmentInput) =>
      apiFetch<YearEnrichmentSettings>('/api/admin/year-enrichment', { method: 'PUT', body: vars }),
    onSuccess: (data) => qc.setQueryData([...YEAR_KEY], data),
  });
}

export function useRunYearBackfill() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiFetch<{ status: string }>('/api/admin/year-enrichment/run', { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: [...YEAR_KEY] }),
  });
}

export function useStopYearBackfill() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiFetch<{ status: string }>('/api/admin/year-enrichment/stop', { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: [...YEAR_KEY] }),
  });
}
