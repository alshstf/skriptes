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
