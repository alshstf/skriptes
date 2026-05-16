import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from './api';

export type KindleTarget = {
  id: number;
  label: string;
  email: string;
  created_at: string;
};

type ListResponse = { items: KindleTarget[] };

const KEY = ['me', 'kindle-targets'] as const;

/**
 * useKindleTargets — список Kindle-адресатов текущего пользователя.
 * Используется на странице профиля и в SendToKindleButton.
 */
export function useKindleTargets() {
  return useQuery<KindleTarget[]>({
    queryKey: [...KEY],
    queryFn: async ({ signal }) => {
      const r = await apiFetch<ListResponse>('/api/me/kindle-targets', { signal });
      return r.items;
    },
    staleTime: 60_000,
  });
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
