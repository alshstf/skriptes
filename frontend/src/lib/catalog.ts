import { useQuery } from '@tanstack/react-query';
import { apiFetch } from './api';
import type { BookListItem } from './books';

export type GenreCount = { code: string; display: string; count: number };
export type SeriesWithCount = { id: number; title: string; count: number };
export type YearCount = { year: number; count: number };

export type Author = {
  id: number;
  last_name: string;
  first_name?: string;
  middle_name?: string;
  full_name: string;
  book_count: number;
  books_total: number;
  top_genres?: GenreCount[];
  series?: SeriesWithCount[];
  books: BookListItem[];
  is_favorite?: boolean;
  /** Распределение книг автора по году добавления в коллекцию. */
  year_stats?: YearCount[];
  /** Сколько книг автора пользователь хотя бы раз скачивал. */
  read_count?: number;
};

export type Series = {
  id: number;
  title: string;
  author_id?: number;
  author_name?: string;
  book_count: number;
  books: BookListItem[];
  is_favorite?: boolean;
  year_stats?: YearCount[];
  read_count?: number;
};

export function useAuthor(id: number | string | undefined) {
  return useQuery<Author>({
    queryKey: ['author', String(id)],
    queryFn: ({ signal }) => apiFetch<Author>(`/api/authors/${id}`, { signal }),
    enabled: id !== undefined && id !== '',
  });
}

export function useSeries(id: number | string | undefined) {
  return useQuery<Series>({
    queryKey: ['series', String(id)],
    queryFn: ({ signal }) => apiFetch<Series>(`/api/series/${id}`, { signal }),
    enabled: id !== undefined && id !== '',
  });
}
