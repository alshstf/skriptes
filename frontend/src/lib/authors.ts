import { useInfiniteQuery, useQuery, keepPreviousData } from '@tanstack/react-query';
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
  /** Уточнение, отличающее тёзок («Блум», «фантаст»); номера librusec бэкенд не отдаёт. */
  note?: string;
  photo_path?: string;
  book_count: number;
  /** Собственные сборники автора (в book_count не входят) — «N сборников» вместо «0 книг». */
  compilation_count?: number;
  is_favorite: boolean;
  favorited_books_count: number;
  top_genres?: GenreCount[];
  languages?: string[];
  years_active?: YearsRange;
  has_adaptations: boolean;
  /** Единый ВНЕШНИЙ рейтинг автора: max по книгам от COALESCE(LIBRATE, web).
   *  Float (web даёт дробные). */
  external_rating?: number;
  /** Источник топ-издания внешнего рейтинга: 'library' | 'googlebooks' |
   *  'openlibrary'. Для тултипа в списке. */
  external_rating_source?: string;
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
  langs?: string[]; // язык ИЗДАНИЯ (books.lang)
  srcLangs?: string[]; // язык ОРИГИНАЛА (books.src_lang) — независимый фильтр
  yearFrom?: number;
  yearTo?: number;
  hasAdaptations?: boolean;
  hasAwards?: boolean; // лауреаты премий — автору или его книге (#447)
  minRating?: number;
  minReaderRating?: number;
  favoritesOnly?: boolean;
  // 'renown' (= '' = дефолт бэка, «Сначала известные») на сервер не шлётся;
  // 'name' — явный алфавит, теперь УХОДИТ в запрос (дефолт сменился).
  sort?: '' | 'renown' | 'name' | 'book_count' | 'rating' | 'reader_rating';
  limit?: number;
  offset?: number;
};

function buildQuery(p: AuthorsListParams): string {
  const sp = new URLSearchParams();
  if (p.query && p.query.trim()) sp.set('q', p.query.trim());
  if (p.genres && p.genres.length > 0) sp.set('genres', p.genres.join(','));
  if (p.langs && p.langs.length > 0) sp.set('langs', p.langs.join(','));
  if (p.srcLangs && p.srcLangs.length > 0) sp.set('src_langs', p.srcLangs.join(','));
  if (p.yearFrom) sp.set('year_from', String(p.yearFrom));
  if (p.yearTo) sp.set('year_to', String(p.yearTo));
  if (p.hasAdaptations) sp.set('has_adaptations', '1');
  if (p.hasAwards) sp.set('has_awards', '1');
  if (p.minRating) sp.set('min_rating', String(p.minRating));
  if (p.minReaderRating) sp.set('min_reader_rating', String(p.minReaderRating));
  if (p.favoritesOnly) sp.set('favorites_only', '1');
  if (p.sort && p.sort !== 'renown') sp.set('sort', p.sort);
  sp.set('limit', String(p.limit ?? 50));
  if (p.offset) sp.set('offset', String(p.offset));
  return sp.toString();
}

/**
 * useAuthorsList — список авторов с фильтрами, постранично (offset, по
 * pageSize). keepPreviousData убирает мерцание между сменой фильтров (как у
 * useSuggest). Общее число бэк считает только для первой страницы (на
 * следующих total = -1, #302) — его держим из первой страницы.
 */
export function useAuthorsList(params: Omit<AuthorsListParams, 'limit' | 'offset'>, pageSize = 50) {
  const qs = buildQuery({ ...params, limit: pageSize });
  return useInfiniteQuery({
    queryKey: ['authors', 'list', qs],
    initialPageParam: 0,
    queryFn: ({ pageParam, signal }) =>
      apiFetch<AuthorListResponse>(`/api/authors?${buildQuery({ ...params, limit: pageSize, offset: pageParam })}`, {
        signal,
      }),
    getNextPageParam: (last, pages) => nextAuthorsPageParam(last, pages, pageSize),
    placeholderData: keepPreviousData,
    staleTime: 30_000,
  });
}

/** AuthorFacets — число авторов на значение фильтра при остальных фильтрах (#389). */
export type AuthorFacets = {
  genres: Record<string, number>;
  genre_categories: Record<string, number>; // по коду категории (cat:sf)
  langs: Record<string, number>;
  src_langs: Record<string, number>;
  adaptations: number;
  awards: number;
};

/**
 * useAuthorFacets — счётчики фильтров /authors. Выбранный фильтр не сужает
 * собственные значения (выбор жанра не обнуляет соседние жанры). Сортировка и
 * страница на числа не влияют — в запрос не идут.
 */
export function useAuthorFacets(params: Omit<AuthorsListParams, 'limit' | 'offset' | 'sort'>) {
  const sp = new URLSearchParams(buildQuery(params));
  sp.delete('limit');
  const qs = sp.toString();
  return useQuery({
    queryKey: ['authors', 'facets', qs],
    queryFn: ({ signal }) => apiFetch<AuthorFacets>(`/api/authors/facets?${qs}`, { signal }),
    placeholderData: keepPreviousData,
    staleTime: 60_000,
  });
}

/**
 * nextAuthorsPageParam — offset следующей страницы или undefined (конец):
 * короткая страница — конец; иначе конец, когда загружено общее число из
 * первой страницы. Вынесен ради unit-тестов.
 */
export function nextAuthorsPageParam(
  last: AuthorListResponse,
  pages: AuthorListResponse[],
  pageSize: number,
): number | undefined {
  if (last.items.length < pageSize) return undefined;
  const loaded = pages.reduce((n, p) => n + p.items.length, 0);
  const total = pages[0]?.total ?? -1;
  if (total >= 0 && loaded >= total) return undefined;
  return loaded;
}
