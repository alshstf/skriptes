import { useQuery } from '@tanstack/react-query';
import { apiFetch } from './api';
import type { BookListItem } from './books';

// Готовые подборки (#389): системные полки на /shelves, состав вычисляет сервер
// из чтения, подписок и экранизаций.

export type Preset = {
  key: string;
  title: string;
  hint: string;
  count: number;
};

export type PresetBooks = {
  items: BookListItem[];
  // Пояснение к работе: id работы → «в серии прочитано 2 из 7», «2027 · «Фильм»».
  notes?: Record<string, string>;
};

const KEY = ['me', 'presets'] as const;

/** usePresets — подборки со счётчиками. */
export function usePresets() {
  return useQuery<Preset[]>({
    queryKey: [...KEY],
    queryFn: async () => (await apiFetch<{ items: Preset[] }>('/api/me/presets')).items,
    staleTime: 60_000,
  });
}

/** usePresetBooks — книги подборки; грузится, когда подборка раскрыта. */
export function usePresetBooks(key: string | null) {
  return useQuery<PresetBooks>({
    queryKey: [...KEY, key ?? 'none'],
    queryFn: () => apiFetch<PresetBooks>(`/api/me/presets/${key}`),
    enabled: key != null,
    staleTime: 60_000,
  });
}
