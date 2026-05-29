/**
 * lib/appearance.ts — персональные настройки внешнего вида.
 *
 * Хранение: сервер (user_settings, /api/me/appearance — синхронно между
 * устройствами) + зеркало в localStorage для мгновенного рендера без
 * вспышки. Чипы читают стиль синхронно из localStorage (useGenreChipStyle),
 * а useAppearance в фоне подтягивает серверное значение и обновляет зеркало.
 */

import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from './api';

export type GenreChipStyle = 'soft' | 'classic';
export type AppearanceSettings = { genre_chip_style: GenreChipStyle };

const DEFAULT: AppearanceSettings = { genre_chip_style: 'soft' };
const LS_KEY = 'skriptes.appearance';
// Кастомное событие для live-обновления в той же вкладке (storage-событие
// браузер шлёт только в другие вкладки).
const EVT = 'skriptes:appearance-changed';

function normalize(s: Partial<AppearanceSettings> | null | undefined): AppearanceSettings {
  return { genre_chip_style: s?.genre_chip_style === 'classic' ? 'classic' : 'soft' };
}

function readLocal(): AppearanceSettings {
  if (typeof window === 'undefined') return DEFAULT;
  try {
    const raw = window.localStorage.getItem(LS_KEY);
    return raw ? normalize(JSON.parse(raw)) : DEFAULT;
  } catch {
    return DEFAULT;
  }
}

function writeLocal(s: AppearanceSettings) {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(LS_KEY, JSON.stringify(s));
  } catch {
    /* private mode / quota — рендер всё равно работает из памяти */
  }
  window.dispatchEvent(new Event(EVT));
}

/**
 * clearAppearanceCache — сбросить локальный кэш внешнего вида. Вызывается на
 * logout: localStorage — per-origin, не per-user, поэтому без сброса
 * следующий пользователь на этом браузере увидел бы стиль предыдущего, пока
 * не подгрузится его серверное значение. После сброса чипы возвращаются к
 * дефолту (событие EVT перечитывает пустой ключ → soft), а серверное
 * значение нового юзера подтянет useAppearance в Layout.
 */
export function clearAppearanceCache() {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.removeItem(LS_KEY);
  } catch {
    /* ignore */
  }
  window.dispatchEvent(new Event(EVT));
}

/**
 * genreChipClass — общий маппинг стиля чипа в className (для `Badge
 * variant="secondary"`). Единый источник для GenreChips и BookListItem,
 * чтобы оба места выглядели одинаково.
 *   soft    — приглушённая заливка, мельче (по умолчанию);
 *   classic — стандартная secondary-плашка (как было раньше).
 */
export function genreChipClass(style: GenreChipStyle): string {
  return style === 'classic'
    ? 'text-xs font-normal'
    : 'text-[11px] font-normal bg-muted text-muted-foreground px-1.5';
}

/**
 * useGenreChipStyle — синхронный реактивный стиль из localStorage. Без
 * react-query (не нужен QueryClient для рендера чипов и тестов) и без
 * вспышки. Обновляется при смене в этой вкладке (custom event) и в других
 * (storage event).
 */
export function useGenreChipStyle(): GenreChipStyle {
  const [style, setStyle] = useState<GenreChipStyle>(() => readLocal().genre_chip_style);
  useEffect(() => {
    const sync = () => setStyle(readLocal().genre_chip_style);
    window.addEventListener(EVT, sync);
    window.addEventListener('storage', sync);
    return () => {
      window.removeEventListener(EVT, sync);
      window.removeEventListener('storage', sync);
    };
  }, []);
  return style;
}

/**
 * useAppearance — серверное состояние (источник правды) + зеркало в
 * localStorage. Монтируется в Layout, чтобы на любой странице подтянуть
 * настройку пользователя и применить её к чипам (через зеркало + событие).
 * initialData из localStorage — мгновенный рендер; initialDataUpdatedAt:0 —
 * сразу освежаем с сервера.
 */
export function useAppearance() {
  return useQuery<AppearanceSettings>({
    queryKey: ['me', 'appearance'],
    queryFn: async () => {
      const r = await apiFetch<AppearanceSettings>('/api/me/appearance');
      const n = normalize(r);
      writeLocal(n);
      return n;
    },
    initialData: readLocal,
    initialDataUpdatedAt: 0,
    staleTime: 60_000,
  });
}

export function useUpdateAppearance() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (vars: AppearanceSettings) => {
      const r = await apiFetch<AppearanceSettings>('/api/me/appearance', {
        method: 'PUT',
        body: vars,
      });
      return normalize(r);
    },
    // Оптимистично применяем сразу (мгновенно в localStorage + событие),
    // чтобы чипы во всём приложении сменились без ожидания ответа.
    onMutate: (vars) => writeLocal(normalize(vars)),
    onSuccess: (data) => {
      writeLocal(data);
      qc.setQueryData(['me', 'appearance'], data);
    },
  });
}
