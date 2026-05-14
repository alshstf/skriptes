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
  is_favorite?: boolean;
};

export type FacetDistribution = Record<string, Record<string, number>>;

export type BookListResponse = {
  items: BookListItem[];
  total: number;
  limit: number;
  offset: number;
  query?: string;
  processing_ms: number;
  facets?: FacetDistribution;
};

export type BookFilters = {
  query: string;
  limit?: number;
  offset?: number;
  genres?: string[];
  lang?: string;
  yearFrom?: number;
  yearTo?: number;
  seriesId?: number;
  authorId?: number;
  sort?: '' | 'year_desc' | 'year_asc' | 'popularity';
  facets?: string[];
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
  is_favorite?: boolean;
};

/**
 * useBooks — список/поиск книг с фильтрами, сортировкой и facet-counts.
 *
 * keepPreviousData чтобы при наборе текста UI не моргал в скелетон между
 * каждым нажатием — старый список остаётся видимым пока не подъедет новый.
 *
 * queryKey включает все параметры — react-query сам инвалидирует кэш
 * при их смене.
 */
export function useBooks(opts: BookFilters) {
  const limit = opts.limit ?? 20;
  const offset = opts.offset ?? 0;
  return useQuery<BookListResponse>({
    queryKey: ['books', { ...opts, limit, offset }],
    queryFn: ({ signal }) => {
      const params = new URLSearchParams();
      if (opts.query) params.set('q', opts.query);
      params.set('limit', String(limit));
      params.set('offset', String(offset));
      if (opts.genres && opts.genres.length > 0) params.set('genres', opts.genres.join(','));
      if (opts.lang) params.set('lang', opts.lang);
      if (opts.yearFrom) params.set('year_from', String(opts.yearFrom));
      if (opts.yearTo) params.set('year_to', String(opts.yearTo));
      if (opts.seriesId) params.set('series_id', String(opts.seriesId));
      if (opts.authorId) params.set('author_id', String(opts.authorId));
      if (opts.sort) params.set('sort', opts.sort);
      if (opts.facets && opts.facets.length > 0) params.set('facets', opts.facets.join(','));
      return apiFetch<BookListResponse>(`/api/books?${params.toString()}`, { signal });
    },
    placeholderData: keepPreviousData,
    staleTime: 10_000,
  });
}

/**
 * useBook — детальная карточка по id.
 *
 * refetchInterval: пока у книги нет cover_path, backend в фоне
 * обогащает её через internal/metadata. Поллим каждые 2 секунды
 * и подменяем плейсхолдер на настоящую обложку без перезагрузки
 * страницы. Сдаёмся через ~20 секунд (10 попыток), чтобы не
 * крутить запросы бесконечно для книг без доступной обложки.
 */
export function useBook(id: number | string | undefined) {
  return useQuery<Book>({
    queryKey: ['book', String(id)],
    queryFn: ({ signal }) => apiFetch<Book>(`/api/books/${id}`, { signal }),
    enabled: id !== undefined && id !== '',
    refetchInterval: (query) => {
      const data = query.state.data as Book | undefined;
      if (data?.cover_path) return false;
      // dataUpdateCount = 1 после первой удачной загрузки → ~10 ретраев.
      if (query.state.dataUpdateCount > 10) return false;
      return 2_000;
    },
  });
}
