import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from './api';

// Пароли устройств (#389): вход читалок по OPDS и синхронизации вместо
// основного пароля. Пароль сервер показывает один раз — при создании.

export type Device = {
  id: number;
  name: string;
  created_at: string;
  last_used_at?: string;
};

export type CreatedDevice = { device: Device; password: string; login: string };

const KEY = ['me', 'devices'] as const;

export function useDevices() {
  return useQuery({
    queryKey: [...KEY],
    queryFn: async ({ signal }) => (await apiFetch<{ items: Device[] }>('/api/me/devices', { signal })).items,
    staleTime: 30_000,
  });
}

export function useCreateDevice() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) =>
      apiFetch<CreatedDevice>('/api/me/devices', { method: 'POST', body: { name } }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: [...KEY] }),
  });
}

export function useDeleteDevice() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => apiFetch<void>(`/api/me/devices/${id}`, { method: 'DELETE' }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: [...KEY] }),
  });
}

/** groupPassword — «abcd efgh jkmn pqrs»: так его легче набрать на читалке. */
export function groupPassword(p: string): string {
  return p.replace(/(.{4})(?=.)/g, '$1 ');
}
