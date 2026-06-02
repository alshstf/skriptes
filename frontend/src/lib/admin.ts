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

// ── Обработка коллекции (парсинг fb2: обложки/аннотации/года + кэш) ──

export type Intensity = 'low' | 'medium' | 'high';

export type CoverCacheSettings = {
  cache_max_mb: number;
  cache_min_free_mb: number;
  // prewarm — МАСТЕР-тумблер всей обработки коллекции (имя поля историческое).
  prewarm: boolean;
  sync_covers: boolean;
  sync_annotations: boolean;
  sync_years: boolean;
  intensity: Intensity;
  // Бюджеты НЕрегенерируемых бакетов (отдельно от обложек книг).
  poster_cache_max_mb: number;
  photo_cache_max_mb: number;
  // статус (read-only): идёт ли проход и какой
  prewarm_running: boolean;
  prewarm_mode: 'off' | 'continuous' | 'once';
  // статистика кэшей (read-only)
  cache_size_bytes: number;
  poster_cache_size_bytes: number;
  photo_cache_size_bytes: number;
  free_bytes: number;
};

// CollectionInput — тело PUT (только конфиг, без read-only полей).
export type CollectionInput = {
  cache_max_mb: number;
  cache_min_free_mb: number;
  prewarm: boolean;
  sync_covers: boolean;
  sync_annotations: boolean;
  sync_years: boolean;
  intensity: Intensity;
  poster_cache_max_mb: number;
  photo_cache_max_mb: number;
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
    mutationFn: (vars: CollectionInput) =>
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

export function useClearPosterCache() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiFetch<{ removed: number }>('/api/admin/cover-cache/clear-posters', { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: [...COVER_KEY] }),
  });
}

export function useClearPhotoCache() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiFetch<{ removed: number }>('/api/admin/cover-cache/clear-photos', { method: 'POST' }),
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
  // режим охвата: false = фолбэк (где fb2 не дал), true = вся коллекция (долго)
  whole_collection: boolean;
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
  whole_collection: boolean;
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

// ── Обложки: дозаполнение cover_path из внешних источников (OL / GB) ──────

export type CoverEnrichmentSettings = {
  enabled: boolean;
  openlibrary: boolean;
  googlebooks: boolean;
  // режим охвата: false = фолбэк (где fb2 не дал), true = вся коллекция (долго)
  whole_collection: boolean;
  openlibrary_rpm: number;
  googlebooks_rpm: number;
  not_found_retry_days: number;
  error_retry_hours: number;
  // статус воркера (read-only)
  cover_backfill_running: boolean;
  cover_backfill_mode: 'off' | 'continuous' | 'once';
  // покрытие cover_path (read-only)
  coverage: {
    total: number;
    with_cover: number;
    by_source: Record<string, number>;
  };
};

// CoverEnrichmentInput — тело PUT (только конфиг, без read-only полей).
export type CoverEnrichmentInput = {
  enabled: boolean;
  openlibrary: boolean;
  googlebooks: boolean;
  whole_collection: boolean;
  openlibrary_rpm: number;
  googlebooks_rpm: number;
  not_found_retry_days: number;
  error_retry_hours: number;
};

const COVER_ENRICH_KEY = ['admin', 'cover-enrichment'] as const;

export function useCoverEnrichmentSettings() {
  return useQuery<CoverEnrichmentSettings>({
    queryKey: [...COVER_ENRICH_KEY],
    queryFn: () => apiFetch<CoverEnrichmentSettings>('/api/admin/cover-enrichment'),
    staleTime: 10_000,
    // Пока воркер идёт — поллим, чтобы видеть рост покрытия и завершение.
    refetchInterval: (query) => (query.state.data?.cover_backfill_running ? 3000 : false),
  });
}

export function useUpdateCoverEnrichmentSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: CoverEnrichmentInput) =>
      apiFetch<CoverEnrichmentSettings>('/api/admin/cover-enrichment', { method: 'PUT', body: vars }),
    onSuccess: (data) => qc.setQueryData([...COVER_ENRICH_KEY], data),
  });
}

export function useRunCoverBackfill() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiFetch<{ status: string }>('/api/admin/cover-enrichment/run', { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: [...COVER_ENRICH_KEY] }),
  });
}

export function useStopCoverBackfill() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiFetch<{ status: string }>('/api/admin/cover-enrichment/stop', { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: [...COVER_ENRICH_KEY] }),
  });
}

// ── Биографии авторов + Экранизации книг (внешние, фоном) ─────────────

export type BioAdaptationSettings = {
  bios: boolean;
  adaptations: boolean;
  bios_rpm: number;
  adaptations_rpm: number;
  // статусы воркеров (read-only)
  bios_running: boolean;
  bios_mode: 'off' | 'continuous' | 'once';
  adaptations_running: boolean;
  adaptations_mode: 'off' | 'continuous' | 'once';
  // покрытие (read-only)
  bio_coverage: { total: number; with_bio: number; with_photo: number };
  adaptation_coverage: { total: number; with_adaptations: number };
};

// BioAdaptationInput — тело PUT (только конфиг, без read-only полей).
export type BioAdaptationInput = {
  bios: boolean;
  adaptations: boolean;
  bios_rpm: number;
  adaptations_rpm: number;
};

const BIO_ADAPT_KEY = ['admin', 'bio-adaptation'] as const;

export function useBioAdaptationSettings() {
  return useQuery<BioAdaptationSettings>({
    queryKey: [...BIO_ADAPT_KEY],
    queryFn: () => apiFetch<BioAdaptationSettings>('/api/admin/bio-adaptation-enrichment'),
    staleTime: 10_000,
    refetchInterval: (query) =>
      query.state.data?.bios_running || query.state.data?.adaptations_running ? 3000 : false,
  });
}

export function useUpdateBioAdaptationSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: BioAdaptationInput) =>
      apiFetch<BioAdaptationSettings>('/api/admin/bio-adaptation-enrichment', { method: 'PUT', body: vars }),
    onSuccess: (data) => qc.setQueryData([...BIO_ADAPT_KEY], data),
  });
}

function bioAdaptationAction(path: string) {
  return function useAction() {
    const qc = useQueryClient();
    return useMutation({
      mutationFn: () =>
        apiFetch<{ status: string }>(`/api/admin/bio-adaptation-enrichment/${path}`, { method: 'POST' }),
      onSuccess: () => qc.invalidateQueries({ queryKey: [...BIO_ADAPT_KEY] }),
    });
  };
}

export const useRunBioBackfill = bioAdaptationAction('bios/run');
export const useStopBioBackfill = bioAdaptationAction('bios/stop');
export const useRunAdaptationBackfill = bioAdaptationAction('adaptations/run');
export const useStopAdaptationBackfill = bioAdaptationAction('adaptations/stop');

