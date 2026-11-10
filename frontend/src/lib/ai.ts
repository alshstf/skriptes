/**
 * lib/ai.ts — ИИ-функции (только при заданном на сервере SKRIPTES_LLM_API_KEY).
 * Без ключа сервер отвечает reason: 'no_key' — кнопок ИИ в интерфейсе нет.
 */
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from './api';

export type AiReason = 'no_key' | 'disabled' | 'user_disabled' | 'budget' | 'quota';

export type AiStatus = {
  available: boolean;
  reason?: AiReason;
  daily_left: number;
};

const STATUS_KEY = ['ai', 'status'] as const;
const ADMIN_KEY = ['admin', 'ai'] as const;

/** Доступны ли ИИ-функции этому пользователю сейчас. */
export function useAiStatus() {
  return useQuery<AiStatus>({
    queryKey: [...STATUS_KEY],
    queryFn: () => apiFetch<AiStatus>('/api/ai/status'),
    staleTime: 60_000,
  });
}

export type AiFeatureSpend = { feature: string; requests: number; actions: number; usd: number };

export type AdminAi = {
  configured: boolean;
  model: string;
  fast_model: string;
  daily_per_user: number;
  budget_usd: number;
  spent_usd: number;
  by_feature: AiFeatureSpend[];
  disabled: boolean;
  disabled_users: number[];
  max_budget_usd: number;
};

export type AdminAiInput = {
  /** null — потолок по умолчанию (из env сервера). */
  monthly_budget_usd: number | null;
  disabled: boolean;
  disabled_users: number[];
};

export function useAdminAi() {
  return useQuery<AdminAi>({
    queryKey: [...ADMIN_KEY],
    queryFn: () => apiFetch<AdminAi>('/api/admin/ai'),
    staleTime: 10_000,
  });
}

export function useUpdateAdminAi() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: AdminAiInput) => apiFetch<AdminAi>('/api/admin/ai', { method: 'PUT', body: vars }),
    onSuccess: (data) => {
      qc.setQueryData([...ADMIN_KEY], data);
      void qc.invalidateQueries({ queryKey: [...STATUS_KEY] });
    },
  });
}

/** Подписи функций в сводке расходов; незнакомая — как есть. */
export const AI_FEATURE_LABELS: Record<string, string> = {
  explain: 'Объяснить',
  who: 'Кто это',
  recap: 'Напомнить, что было',
  define: 'Что значит',
  translate: 'Перевести',
  summary: 'Пересказы книг',
};

export function formatUsd(v: number): string {
  return `${v.toLocaleString('ru-RU', { minimumFractionDigits: 2, maximumFractionDigits: 2 })} $`;
}

/** Разбор поля потолка: число от 0 до max; запятая — тоже десятичный разделитель. */
export function parseBudget(raw: string, max: number): number | null {
  const s = raw.trim().replace(',', '.');
  if (!/^\d+(\.\d{1,2})?$/.test(s)) return null;
  const v = Number(s);
  return v >= 0 && v <= max ? v : null;
}
