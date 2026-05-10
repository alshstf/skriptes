import { useQuery, keepPreviousData } from '@tanstack/react-query';
import { apiFetch } from './api';

export type BookListItem = {
  id: number;
  title: string;
  authors: string[];
  series?: string;
  genres?: string[];
  year?: number;
  lang?: string;
  lib_id: string;
};

export type BookListResponse = {
  items: BookListItem[];
  total: number;
  limit: number;
  offset: number;
  query?: string;
  processing_ms: number;
};

export type AuthorRef = {
  id: number;
  last_name: string;
  first_name?: string;
  middle_name?: string;
  full_name: string;
};

export type SeriesRef = { id: number; title: string };

export type GenreRef = {
  id: number;
  code: string;
  name_ru?: string;
  name_en?: string;
  display: string;
};

export type Book = {
  id: number;
  lib_id: string;
  title: string;
  authors: AuthorRef[];
  series?: SeriesRef;
  ser_no?: number;
  genres: GenreRef[];
  lang?: string;
  date_added?: string;
  rating?: number;
  annotation?: string;
  cover_path?: string;
  archive: string;
  file_name: string;
  ext: string;
  size_bytes: number;
  deleted?: boolean;
};

/**
 * useBooks — список/поиск книг.
 * keepPreviousData чтобы при наборе текста UI не моргал в скелетон между
 * каждым нажатием — старый список остаётся видимым пока не подъедет новый.
 */
export function useBooks(opts: { query: string; limit?: number; offset?: number }) {
  const limit = opts.limit ?? 20;
  const offset = opts.offset ?? 0;
  return useQuery<BookListResponse>({
    queryKey: ['books', { query: opts.query, limit, offset }],
    queryFn: ({ signal }) => {
      const params = new URLSearchParams();
      if (opts.query) params.set('q', opts.query);
      params.set('limit', String(limit));
      params.set('offset', String(offset));
      return apiFetch<BookListResponse>(`/api/books?${params.toString()}`, { signal });
    },
    placeholderData: keepPreviousData,
    staleTime: 10_000,
  });
}

/** useBook — детальная карточка по id. */
export function useBook(id: number | string | undefined) {
  return useQuery<Book>({
    queryKey: ['book', String(id)],
    queryFn: ({ signal }) => apiFetch<Book>(`/api/books/${id}`, { signal }),
    enabled: id !== undefined && id !== '',
  });
}
