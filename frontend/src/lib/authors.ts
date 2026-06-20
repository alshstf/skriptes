import { useQuery, keepPreviousData } from '@tanstack/react-query';
import { apiFetch } from './api';

/**
 * lib/authors.ts — список авторов с фильтрами (раздел «Авторы», /authors).
 *
 * NB: карточка ОДНОГО автора живёт в lib/catalog.ts (useAuthor) — это другой
 * хук с другим query-ключом, не путать. Здесь — постраничный список с
 * агрегатами (GET /api/authors).
 */

/** GenreCount — топ-жанр автора (как в карточке автора). */
export type GenreCount = {
  code: string;
  display: string;
  count: number;
};

/** YearsRange — диапазон лет активности автора (по году написания). */
export type YearsRange = {
  from: number;
  to: number;
};

/** AuthorListItem — строка списка авторов с агрегатами. */
export type AuthorListItem = {
  id: number;
  full_name: string;
  photo_path?: string;
  book_count: number;
  is_favorite: boolean;
  favorited_books_count: number;
  top_genres?: GenreCount[];
  languages?: string[];
  years_active?: YearsRange;
  has_adaptations: boolean;
  library_rating?: number;
  /** Средняя оценка читателей (book_ratings) по работам автора, по инстансу. */
  reader_rating?: number;
  /** Число пользовательских оценок (для бейджа «N оценок»). */
  reader_rating_count?: number;
};

export type AuthorListResponse = {
  items: AuthorListItem[];
  total: number;
};

/** AuthorsListParams — параметры фильтрации/сортировки/пагинации. */
export type AuthorsListParams = {
  query?: string;
  genres?: string[];
  langs?: string[];
  yearFrom?: number;
  yearTo?: number;
  hasAdaptations?: boolean;
  minRating?: number;
  minReaderRating?: number;
  favoritesOnly?: boolean;
  sort?: '' | 'name' | 'book_count' | 'rating' | 'reader_rating';
  limit?: number;
  offset?: number;
};

function buildQuery(p: AuthorsListParams): string {
  const sp = new URLSearchParams();
  if (p.query && p.query.trim()) sp.set('q', p.query.trim());
  if (p.genres && p.genres.length > 0) sp.set('genres', p.genres.join(','));
  if (p.langs && p.langs.length > 0) sp.set('langs', p.langs.join(','));
  if (p.yearFrom) sp.set('year_from', String(p.yearFrom));
  if (p.yearTo) sp.set('year_to', String(p.yearTo));
  if (p.hasAdaptations) sp.set('has_adaptations', '1');
  if (p.minRating) sp.set('min_rating', String(p.minRating));
  if (p.minReaderRating) sp.set('min_reader_rating', String(p.minReaderRating));
  if (p.favoritesOnly) sp.set('favorites_only', '1');
  if (p.sort && p.sort !== 'name') sp.set('sort', p.sort);
  sp.set('limit', String(p.limit ?? 50));
  if (p.offset) sp.set('offset', String(p.offset));
  return sp.toString();
}

/**
 * useAuthorsList — список авторов с фильтрами. keepPreviousData убирает
 * мерцание между сменой фильтров (как у useSuggest). Пагинация — через
 * limit/offset (PG-backed, без Meili).
 */
export function useAuthorsList(params: AuthorsListParams) {
  const qs = buildQuery(params);
  return useQuery<AuthorListResponse>({
    queryKey: ['authors', 'list', qs],
    queryFn: ({ signal }) => apiFetch<AuthorListResponse>(`/api/authors?${qs}`, { signal }),
    placeholderData: keepPreviousData,
    staleTime: 30_000,
  });
}
