import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { apiFetch } from './api';

/**
 * searchLog.ts — история поиска пользователя (план recsys/01, задача 1.4).
 * Пишет бэк при поиске в списке книг (search_log_id в ответе /api/books);
 * здесь — клик из выдачи, страница «Поиск» в профиле, удаление. Видит только
 * сам пользователь; срок хранения — retention_days (0 — лог выключен).
 */

export type SearchLogEntry = {
  id: number;
  q: string;
  hits: number;
  clicked_work_id?: number;
  clicked_title?: string;
  at: string;
};

export type SearchLogResponse = { items: SearchLogEntry[]; total: number; retention_days: number };

export const SEARCH_LOG_KEY = ['me', 'search-log'] as const;

export function useSearchLog(limit = 200) {
  return useQuery<SearchLogResponse>({
    queryKey: [...SEARCH_LOG_KEY, limit],
    queryFn: ({ signal }) => apiFetch<SearchLogResponse>(`/api/me/search-log?limit=${limit}`, { signal }),
    staleTime: 10_000,
  });
}

export function useDeleteSearchLogEntry() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => apiFetch<void>(`/api/me/search-log/${id}`, { method: 'DELETE' }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: SEARCH_LOG_KEY }),
    onError: () => toast.error('Не удалось удалить'),
  });
}

export function useClearSearchLog() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => apiFetch<void>('/api/me/search-log', { method: 'DELETE' }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: SEARCH_LOG_KEY });
      void qc.invalidateQueries({ queryKey: ['me', 'recs'] });
      toast.success('История поиска удалена');
    },
    onError: () => toast.error('Не удалось удалить историю'),
  });
}

/**
 * logSearchClick — переход из выдачи к книге: fire-and-forget, ошибки
 * глотаем (навигация важнее). logId — search_log_id из ответа списка.
 */
export function logSearchClick(logId: number | undefined, workId: number) {
  if (!logId) return;
  void apiFetch<void>(`/api/me/search-log/${logId}/click`, {
    method: 'POST',
    body: { work_id: workId },
  }).catch(() => undefined);
}
