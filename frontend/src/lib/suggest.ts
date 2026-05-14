import { useQuery, keepPreviousData } from '@tanstack/react-query';
import { apiFetch } from './api';
import type { BookListItem } from './books';

export type AuthorSuggest = {
  id: number;
  full_name: string;
  book_count: number;
};

export type SeriesSuggest = {
  id: number;
  title: string;
  author_name?: string;
  book_count: number;
};

export type SuggestResponse = {
  query: string;
  books: BookListItem[];
  authors: AuthorSuggest[];
  series: SeriesSuggest[];
};

const EMPTY: SuggestResponse = { query: '', books: [], authors: [], series: [] };

/**
 * useSuggest — typeahead для command-palette.
 *
 * Запрос едет только если query ≥ 2 символов: меньшие префиксы дают
 * слишком много шума и нагружают backend без пользы. При пустом query
 * возвращаем стабильный EMPTY (тот же reference) — компонент Command
 * ниже не делает лишних рендеров.
 *
 * keepPreviousData предотвращает мерцание между нажатиями: пока новый
 * ответ грузится, в палитре остаётся прошлый список.
 *
 * staleTime=0 — без этого свежий сигнал персонализации (новый view
 * после открытия карточки книги, новый favorite) "застывает" в кэше
 * до 5 секунд, и пользователь видит старый порядок выдачи. На нашей
 * нагрузке (домашний сервер) лишний запрос при каждом открытии
 * палитры — не проблема.
 */
export function useSuggest(query: string, limit = 5) {
  const trimmed = query.trim();
  const enabled = trimmed.length >= 2;
  return useQuery<SuggestResponse>({
    queryKey: ['suggest', { q: trimmed, limit }],
    queryFn: ({ signal }) => {
      const params = new URLSearchParams({ q: trimmed, limit: String(limit) });
      return apiFetch<SuggestResponse>(`/api/search/suggest?${params.toString()}`, { signal });
    },
    enabled,
    placeholderData: keepPreviousData,
    staleTime: 0,
  });
}

export const EMPTY_SUGGEST = EMPTY;
