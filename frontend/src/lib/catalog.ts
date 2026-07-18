import { useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from './api';
import type { BookListItem } from './books';

export type GenreCount = { code: string; display: string; count: number };
// all_compilations — серия целиком из сборников/антологий/томов собраний
// (works.kind у всех работ): уводится из списка серий автора в секцию
// «Сборники и антологии» внизу карточки.
export type SeriesWithCount = { id: number; title: string; count: number; all_compilations?: boolean };
export type YearBook = { id: number; title: string };
/** Точка гистограммы «по годам написания»: год, число книг и сами книги
 *  (для тултипа — что именно написано в этот год). */
export type YearCount = { year: number; count: number; books?: YearBook[] };

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
  /** Агрегаты-зеркало строки списка авторов (чтобы карточка несла ту же сводку). */
  external_rating?: number;
  /** Источник топ-издания внешнего рейтинга: 'library' | 'googlebooks' | 'openlibrary'. */
  external_rating_source?: string;
  reader_rating?: number;
  reader_rating_count?: number;
  has_adaptations?: boolean;
  /** Языки изданий (lang∪src_lang), нормализованные, по убыванию числа книг. */
  languages?: string[];
  /** Диапазон лет активности (по written_year). */
  years_active?: { from: number; to: number };
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
  /** «Служебный автор» (агрегат-псевдоавтор): скрыт из списка /authors;
   *  правится admin-переключателем на карточке. */
  is_service?: boolean;
  /** Запрос инициировал ленивое дозаполнение года (порядок книг в серии мог
   *  «упасть» на фолбэк) — фронт поллит и переставляет порядок по series_order. */
  year_enrichment_pending?: boolean;
};

export type SeriesAuthorRef = { id: number; name: string };

/**
 * useAuthorSeries — серии автора (для пикера переноса серии: листим серии того же
 * автора без поиска). Грузим лениво (enabled — только при открытом поповере).
 */
export function useAuthorSeries(authorId: number | undefined, enabled: boolean) {
  return useQuery({
    queryKey: ['author-series', authorId],
    enabled: enabled && !!authorId,
    queryFn: () =>
      apiFetch<{ items: SeriesWithCount[] }>(`/api/authors/${authorId}/series`).then((r) => r.items),
    staleTime: 60_000,
  });
}

export type Series = {
  id: number;
  title: string;
  author_id?: number;
  author_name?: string;
  /** Все авторы книг серии (серия может содержать книги нескольких авторов). */
  authors?: SeriesAuthorRef[];
  book_count: number;
  books: BookListItem[];
  is_favorite?: boolean;
  year_stats?: YearCount[];
  read_count?: number;
  /** См. Author.year_enrichment_pending. */
  year_enrichment_pending?: boolean;
};

/**
 * useAuthor — детальная карточка автора с лениво подгружаемыми bio + photo.
 *
 * Аналогично useBook: refetchInterval поллит пока сервер не закончил
 * enrichment (Wikipedia). Сдаёмся после ~10 попыток, чтобы фронт мог
 * показать fallback "Описание отсутствует" вместо вечного скелетона.
 */
const AUTHOR_ENRICH_MAX_TRIES = 10;
const SERIES_ENRICH_MAX_TRIES = 10;

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
      // bio/фото «осели», когда оба пришли ИЛИ бэкенд пометил enrichment_fetched
      // (даже если не нашлись) — иначе висели бы скелетоном и долбили внешние API.
      const metaSettled = (havePhoto && haveBio) || !!data?.enrichment_fetched;
      // Год серии ещё подтягивается → продолжаем поллить, чтобы переставить порядок.
      if (metaSettled && !data?.year_enrichment_pending) return false;
      if (q.state.dataUpdateCount > AUTHOR_ENRICH_MAX_TRIES) return false;
      return 2_000;
    },
  });

  const state = qc.getQueryState<Author>([...queryKey]);
  const enrichmentExhausted =
    !!state?.data &&
    (state.data.enrichment_fetched || state.dataUpdateCount > AUTHOR_ENRICH_MAX_TRIES) &&
    (!state.data.photo_path || !state.data.bio);

  return { ...query, enrichmentExhausted };
}

export function useSeries(id: number | string | undefined) {
  return useQuery<Series>({
    queryKey: ['series', String(id)],
    queryFn: ({ signal }) => apiFetch<Series>(`/api/series/${id}`, { signal }),
    enabled: id !== undefined && id !== '',
    // Поллим, пока сервер дозаполняет год книгам серии без порядка — чтобы
    // переставить их по series_order. Кап попыток, как у автора.
    refetchInterval: (q) => {
      const data = q.state.data as Series | undefined;
      if (!data?.year_enrichment_pending) return false;
      if (q.state.dataUpdateCount > SERIES_ENRICH_MAX_TRIES) return false;
      return 2_000;
    },
  });
}
