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
import type { QueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { apiFetch } from './api';
import { useMe } from './auth';
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

// useResetYearLookups — сброс неудачных попыток (not_found/error) по году:
// книги перепроверятся на следующем проходе (напр. после улучшения поиска).
export function useResetYearLookups() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiFetch<{ reset: number }>('/api/admin/year-enrichment/reset-failed', { method: 'POST' }),
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

// useResetCoverLookups — сброс неудачных попыток (not_found/error) по обложкам.
export function useResetCoverLookups() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiFetch<{ reset: number }>('/api/admin/cover-enrichment/reset-failed', { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: [...COVER_ENRICH_KEY] }),
  });
}

// ── Внешний рейтинг (Google Books / OpenLibrary, фоном) ───────────────

export type ExternalRatingSettings = {
  enabled: boolean;
  googlebooks: boolean;
  openlibrary: boolean;
  // режим охвата: false = только пробелы (без любого рейтинга), true = вся коллекция
  whole_collection: boolean;
  googlebooks_rpm: number;
  openlibrary_rpm: number;
  not_found_retry_days: number;
  error_retry_hours: number;
  // статус воркера (read-only)
  external_rating_running: boolean;
  external_rating_mode: 'off' | 'continuous' | 'once';
  // покрытие (read-only)
  coverage: {
    total: number;
    with_rating: number; // LIBRATE или web
    with_web: number; // только web (external_rating)
    by_source: Record<string, number>;
  };
};

// ExternalRatingInput — тело PUT (только конфиг, без read-only полей).
export type ExternalRatingInput = {
  enabled: boolean;
  googlebooks: boolean;
  openlibrary: boolean;
  whole_collection: boolean;
  googlebooks_rpm: number;
  openlibrary_rpm: number;
  not_found_retry_days: number;
  error_retry_hours: number;
};

const EXTERNAL_RATING_KEY = ['admin', 'external-rating'] as const;

export function useExternalRatingSettings() {
  return useQuery<ExternalRatingSettings>({
    queryKey: [...EXTERNAL_RATING_KEY],
    queryFn: () => apiFetch<ExternalRatingSettings>('/api/admin/external-rating'),
    staleTime: 10_000,
    refetchInterval: (query) => (query.state.data?.external_rating_running ? 3000 : false),
  });
}

export function useUpdateExternalRatingSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: ExternalRatingInput) =>
      apiFetch<ExternalRatingSettings>('/api/admin/external-rating', { method: 'PUT', body: vars }),
    onSuccess: (data) => qc.setQueryData([...EXTERNAL_RATING_KEY], data),
  });
}

export function useRunExternalRating() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiFetch<{ status: string }>('/api/admin/external-rating/run', { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: [...EXTERNAL_RATING_KEY] }),
  });
}

export function useStopExternalRating() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiFetch<{ status: string }>('/api/admin/external-rating/stop', { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: [...EXTERNAL_RATING_KEY] }),
  });
}

// useResetRatingLookups — сброс неудачных попыток (not_found/error) по внешнему рейтингу.
export function useResetRatingLookups() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiFetch<{ reset: number }>('/api/admin/external-rating/reset-failed', { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: [...EXTERNAL_RATING_KEY] }),
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

// ── Группировка изданий в логические книги (works) ───────────────────────

export type WorkGroupingSettings = {
  enabled: boolean;
  openlibrary: boolean;
  wikidata: boolean;
  // режим охвата: false = только книги с локальным edition-сканом, true = все
  whole_collection: boolean;
  openlibrary_rpm: number;
  wikidata_rpm: number;
  not_found_retry_days: number;
  error_retry_hours: number;
  // статус воркера (read-only)
  work_grouping_running: boolean;
  work_grouping_mode: 'off' | 'continuous' | 'once';
  // идёт массовый разбор работ (POST /admin/works/regroup): воркер приостановлен
  // инструментом, попытки включения встанут в очередь — свитчер дизейблим.
  // done/total — прогресс разбора в работах (счётчик «обработано N из M»).
  work_regroup_running?: boolean;
  work_regroup_done?: number;
  work_regroup_total?: number;
  // покрытие (read-only)
  coverage: {
    books: number;
    works: number;
    multi_edition_works: number;
    scanned: number;
  };
};

// WorkGroupingInput — тело PUT (только конфиг, без read-only полей).
export type WorkGroupingInput = {
  enabled: boolean;
  openlibrary: boolean;
  wikidata: boolean;
  whole_collection: boolean;
  openlibrary_rpm: number;
  wikidata_rpm: number;
  not_found_retry_days: number;
  error_retry_hours: number;
};

const WORK_GROUP_KEY = ['admin', 'work-grouping'] as const;

export function useWorkGroupingSettings() {
  return useQuery<WorkGroupingSettings>({
    queryKey: [...WORK_GROUP_KEY],
    queryFn: () => apiFetch<WorkGroupingSettings>('/api/admin/work-grouping'),
    staleTime: 10_000,
    // Поллим и во время фонового воркера, и во время массового разбора работ
    // (regroup) — индикатор и разблокировка контролов обновляются сами.
    refetchInterval: (query) =>
      query.state.data?.work_grouping_running || query.state.data?.work_regroup_running
        ? 3000
        : false,
  });
}

export function useUpdateWorkGroupingSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: WorkGroupingInput) =>
      apiFetch<WorkGroupingSettings>('/api/admin/work-grouping', { method: 'PUT', body: vars }),
    onSuccess: (data) => qc.setQueryData([...WORK_GROUP_KEY], data),
  });
}

export function useRunWorkGrouping() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiFetch<{ status: string }>('/api/admin/work-grouping/run', { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: [...WORK_GROUP_KEY] }),
  });
}

export function useStopWorkGrouping() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiFetch<{ status: string }>('/api/admin/work-grouping/stop', { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: [...WORK_GROUP_KEY] }),
  });
}

// Отмена идущего массового разбора работ (regroup) — если подвис или идёт
// дольше ожидаемого. Обработанные авторы остаются разобранными (и синкнутся
// в поиск), воркер восстановится автоматически.
export function useStopWorksRegroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiFetch<{ status: string }>('/api/admin/works/regroup/stop', { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: [...WORK_GROUP_KEY] }),
  });
}

// ── Ручные merge/split с карточек (admin) ─────────────────────────────────
//
// merge оперирует РАБОТАМИ (объединяет несколько works в одну), split —
// ИЗДАНИЯМИ (выносит book_id'ы в новую работу). Обе ручки гейтятся requireAdmin
// на бэке. После успеха инвалидируем все каталожные кэши, чтобы карточки
// серии/автора/книги/списка пере-схлопнулись.

// invalidateCatalog — сбросить кэши, на которые влияет merge/split/оверрайд.
function invalidateCatalog(qc: QueryClient) {
  // ['author'] — карточка автора; ['authors'] — список /authors (агрегаты автора
  // меняются при правке авторов/жанров книги).
  for (const key of [['series'], ['author'], ['authors'], ['book'], ['work'], ['books-infinite']]) {
    void qc.invalidateQueries({ queryKey: key });
  }
}

/** useMergeWorks — объединить работы в одну (work_ids ≥ 2; target опционален). */
export function useMergeWorks() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { work_ids: number[]; target?: number }) =>
      apiFetch<{ work_id: number }>('/api/admin/works/merge', { method: 'POST', body: vars }),
    onSuccess: () => {
      invalidateCatalog(qc);
      toast.success('Издания объединены в одну книгу');
    },
    onError: (e) =>
      toast.error(`Не удалось объединить: ${e instanceof Error ? e.message : 'ошибка'}`),
  });
}

/** useSplitEditions — вынести издания (book_ids) в новую отдельную работу. */
export function useSplitEditions() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { book_ids: number[] }) =>
      apiFetch<{ work_id: number }>('/api/admin/works/split', { method: 'POST', body: vars }),
    onSuccess: () => {
      invalidateCatalog(qc);
      toast.success('Издания вынесены в отдельную книгу');
    },
    onError: (e) =>
      toast.error(`Не удалось разделить: ${e instanceof Error ? e.message : 'ошибка'}`),
  });
}

// ── Локальные оверрайды метаданных (admin, глобально) ─────────────────────
//
// Ручная корректура полей книги/работы. Значение материализуется в реальную
// колонку на бэке → попадает в поиск/фильтры; после успеха инвалидируем
// каталог + список оверрайдов (для индикаторов «изменено»).

// OverridesMap — какие поля работы и её изданий оверрайднуты (для индикаторов).
export type OverridesMap = { work: string[]; book: Record<string, string[]> };

/** useOverrides — список правок работы и её изданий (только админ). */
export function useOverrides(workId: number | undefined) {
  const me = useMe();
  return useQuery({
    queryKey: ['overrides', workId],
    enabled: me.data?.role === 'admin' && !!workId,
    queryFn: () => apiFetch<OverridesMap>(`/api/admin/overrides?work_id=${workId}`),
  });
}

function invalidateOverrides(qc: QueryClient) {
  invalidateCatalog(qc);
  void qc.invalidateQueries({ queryKey: ['overrides'] });
}

type OverrideTarget = { target_kind: 'book' | 'work'; target_id: number; field: string };

/** useSetOverride — выставить правку поля (value = {v: …} или составной объект). */
export function useSetOverride() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: OverrideTarget & { value: unknown }) =>
      apiFetch<{ ok: boolean }>('/api/admin/overrides', { method: 'POST', body: vars }),
    onSuccess: () => {
      invalidateOverrides(qc);
      toast.success('Поле обновлено');
    },
    onError: (e) => toast.error(`Не удалось сохранить: ${e instanceof Error ? e.message : 'ошибка'}`),
  });
}

/** useRevertOverride — отменить правку поля (вернуть оригинал). */
export function useRevertOverride() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: OverrideTarget) =>
      apiFetch<{ ok: boolean }>('/api/admin/overrides', { method: 'DELETE', body: vars }),
    onSuccess: () => {
      invalidateOverrides(qc);
      toast.success('Правка отменена');
    },
    onError: (e) => toast.error(`Не удалось отменить: ${e instanceof Error ? e.message : 'ошибка'}`),
  });
}

/** useRevertAllOverrides — отменить все правки книги. */
export function useRevertAllOverrides() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { book_id: number }) =>
      apiFetch<{ ok: boolean }>('/api/admin/overrides/revert-all', { method: 'POST', body: vars }),
    onSuccess: () => {
      invalidateOverrides(qc);
      toast.success('Все правки книги отменены');
    },
    onError: (e) => toast.error(`Не удалось: ${e instanceof Error ? e.message : 'ошибка'}`),
  });
}

// ── «Выключатели» ленивого обогащения (режим «Выкл» по типам) ─────────────
//
// Отдельная ось от фоновых воркеров: эти флаги подавляют ИНИЦИАЦИЮ нового
// lazy-фетча соответствующего типа при открытии карточки. Год сюда не входит
// (у него нет lazy-пути). Дефолт всё false = lazy работает для всех типов.

export type EnrichmentGates = {
  cover_disabled: boolean;
  annotation_disabled: boolean;
  author_disabled: boolean;
  adaptation_disabled: boolean;
};

const GATES_KEY = ['admin', 'enrichment-gates'] as const;

export function useEnrichmentGates() {
  return useQuery<EnrichmentGates>({
    queryKey: [...GATES_KEY],
    queryFn: () => apiFetch<EnrichmentGates>('/api/admin/enrichment-gates'),
    staleTime: 10_000,
  });
}

export function useUpdateEnrichmentGates() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: EnrichmentGates) =>
      apiFetch<EnrichmentGates>('/api/admin/enrichment-gates', { method: 'PUT', body: vars }),
    onSuccess: (data) => qc.setQueryData([...GATES_KEY], data),
  });
}

