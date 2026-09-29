import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from './api';

export type KindleTarget = {
  id: number;
  label: string;
  email: string;
  created_at: string;
};

/** sender — адрес, с которого сервер отправляет книги (пусто — отправка не настроена). */
type ListResponse = { items: KindleTarget[]; sender?: string };

const KEY = ['me', 'kindle-targets'] as const;

// Один запрос на оба хука: список и адрес отправителя приходят вместе.
const listQuery = {
  queryKey: [...KEY],
  queryFn: ({ signal }: { signal: AbortSignal }) =>
    apiFetch<ListResponse>('/api/me/kindle-targets', { signal }),
  staleTime: 60_000,
};

/**
 * useKindleTargets — список Kindle-адресатов текущего пользователя.
 * Используется на странице профиля и в SendToKindleButton.
 */
export function useKindleTargets() {
  return useQuery({ ...listQuery, select: (r: ListResponse) => r.items });
}

/**
 * useKindleSender — адрес, с которого сервер отправляет книги на Kindle
 * (SKRIPTES_SMTP_FROM, иначе логин SMTP). Его добавляют в «Утверждённые
 * отправители» Amazon. Пустая строка — отправка на сервере не настроена.
 */
export function useKindleSender() {
  return useQuery({ ...listQuery, select: (r: ListResponse) => r.sender ?? '' });
}

export function useAddKindleTarget() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { label: string; email: string }) =>
      apiFetch<KindleTarget>('/api/me/kindle-targets', { method: 'POST', body: vars }),
    onSuccess: () => qc.invalidateQueries({ queryKey: [...KEY] }),
  });
}

export function useUpdateKindleTarget() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { id: number; label: string; email: string }) =>
      apiFetch<KindleTarget>(`/api/me/kindle-targets/${vars.id}`, {
        method: 'PATCH',
        body: { label: vars.label, email: vars.email },
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: [...KEY] }),
  });
}

export function useDeleteKindleTarget() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) =>
      apiFetch<void>(`/api/me/kindle-targets/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: [...KEY] }),
  });
}

/**
 * useSendToKindle — отправляет конкретную книгу на конкретный target.
 * Возвращает мутацию: caller дёргает .mutate({bookId, targetId}).
 */
export function useSendToKindle() {
  return useMutation({
    mutationFn: (vars: { bookId: number; targetId: number }) =>
      apiFetch<{ status: string; to: string }>(`/api/books/${vars.bookId}/send-to-kindle`, {
        method: 'POST',
        body: { target_id: vars.targetId },
      }),
  });
}
