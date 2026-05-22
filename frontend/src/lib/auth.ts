import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch, ApiError } from './api';

export type Role = 'admin' | 'user';

export type User = {
  id: number;
  email: string;
  display_name: string;
  role: Role;
  kindle_email?: string;
  created_at: string;
};

export type MeResponse = { user: User };

const meQueryKey = ['auth', 'me'] as const;

/**
 * useMe — текущий пользователь, если есть валидная сессия.
 * - 401 трактуется как "не авторизован" (null), а НЕ как ошибка запроса:
 *   так компонентам не нужно хранить error отдельно от data.
 * - retry отключен: сессия либо валидна, либо нет — повторять бессмысленно.
 */
export function useMe() {
  return useQuery<User | null>({
    queryKey: meQueryKey,
    queryFn: async () => {
      try {
        const r = await apiFetch<MeResponse>('/api/auth/me');
        return r.user;
      } catch (err) {
        if (err instanceof ApiError && err.isUnauthorized()) {
          return null;
        }
        throw err;
      }
    },
    retry: false,
    staleTime: 60_000,
  });
}

/**
 * useLogin — мутация. На успехе кладёт user в кэш useMe, чтобы
 * последующая навигация не делала лишний раунд-трип.
 */
export function useLogin() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (vars: { email: string; password: string }) => {
      const r = await apiFetch<MeResponse>('/api/auth/login', { method: 'POST', body: vars });
      return r.user;
    },
    onSuccess: (user) => {
      qc.setQueryData(meQueryKey, user);
    },
  });
}

/**
 * useLogout — мутация. Чистит кэш auth и invalidate-ит всё остальное
 * чтобы свежевошедший пользователь не увидел кэш предыдущего.
 */
export function useLogout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      await apiFetch<void>('/api/auth/logout', { method: 'POST' });
    },
    onSettled: async () => {
      qc.setQueryData(meQueryKey, null);
      await qc.invalidateQueries();
    },
  });
}

/**
 * useUpdateMe — обновляет свой display_name и/или email. Пустые поля
 * = «не менять». На успехе подменяем user в кэше useMe, чтобы header
 * с display_name'ом перерендерился сразу без отдельного GET.
 */
export function useUpdateMe() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (vars: { display_name?: string; email?: string }) => {
      const r = await apiFetch<MeResponse>('/api/me', { method: 'PATCH', body: vars });
      return r.user;
    },
    onSuccess: (user) => {
      qc.setQueryData(meQueryKey, user);
    },
  });
}

/**
 * useChangeMyPassword — меняет свой пароль с верификацией текущего.
 * Backend сохраняет текущую сессию (cookie совпадает с keepSessionToken)
 * и революует остальные. Кэш useMe трогать не нужно — user-объект
 * не меняется. Ошибки:
 *   - 403 → текущий пароль неверный (показать сообщение в форме)
 *   - 400 → новый пароль короче 8 символов
 */
export function useChangeMyPassword() {
  return useMutation({
    mutationFn: async (vars: { current_password: string; new_password: string }) => {
      await apiFetch<void>('/api/me/password', { method: 'PATCH', body: vars });
    },
  });
}
