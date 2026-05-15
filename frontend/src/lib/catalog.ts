import { useQuery, useQueryClient } from '@tanstack/react-query';
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
  /** Био-текст из Wikipedia (lazy enrichment). */
  bio?: string;
  /** sha256.ext в /cache/covers — фото автора. Отдаётся через /api/covers. */
  photo_path?: string;
  /** Была ли попытка enrichment'а (для UI fallback "Описание отсутствует"). */
  enrichment_fetched?: boolean;
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

/**
 * useAuthor — детальная карточка автора с лениво подгружаемыми bio + photo.
 *
 * Аналогично useBook: refetchInterval поллит пока сервер не закончил
 * enrichment (Wikipedia). Сдаёмся после ~10 попыток, чтобы фронт мог
 * показать fallback "Описание отсутствует" вместо вечного скелетона.
 */
const AUTHOR_ENRICH_MAX_TRIES = 10;

export function useAuthor(id: number | string | undefined) {
  const qc = useQueryClient();
  const queryKey = ['author', String(id)] as const;

  const query = useQuery<Author>({
    queryKey: [...queryKey],
    queryFn: ({ signal }) => apiFetch<Author>(`/api/authors/${id}`, { signal }),
    enabled: id !== undefined && id !== '',
    refetchInterval: (q) => {
      const data = q.state.data as Author | undefined;
      const havePhoto = !!data?.photo_path;
      const haveBio = !!data?.bio;
      if (havePhoto && haveBio) return false;
      if (q.state.dataUpdateCount > AUTHOR_ENRICH_MAX_TRIES) return false;
      return 2_000;
    },
  });

  const state = qc.getQueryState<Author>([...queryKey]);
  const enrichmentExhausted =
    !!state?.data &&
    state.dataUpdateCount > AUTHOR_ENRICH_MAX_TRIES &&
    (!state.data.photo_path || !state.data.bio);

  return { ...query, enrichmentExhausted };
}

export function useSeries(id: number | string | undefined) {
  return useQuery<Series>({
    queryKey: ['series', String(id)],
    queryFn: ({ signal }) => apiFetch<Series>(`/api/series/${id}`, { signal }),
    enabled: id !== undefined && id !== '',
  });
}
