import {
  useMutation,
  useQuery,
  useInfiniteQuery,
  useQueryClient,
  keepPreviousData,
} from '@tanstack/react-query';
import { apiFetch } from './api';

export type BookListItem = {
  id: number;
  title: string;
  authors: string[];
  series?: string;
  series_id?: number;
  ser_no?: number;
  // series_order — 0-based позиция книги внутри серии после backend-каскада
  // сортировки (ser_no → written_year → edition_year → эвристика → date_added).
  // Заполняется на карточках автора/серии; в /books-листинге отсутствует.
  series_order?: number;
  genres?: string[];
  year?: number;
  lang?: string;
  lib_id: string;
  is_favorite?: boolean;
  // cover_path догидрачивается backend'ом из Postgres (в Meili-индексе
  // обложек нет). Пусто, если обложка ещё не обогащена → placeholder.
  cover_path?: string;
  // edition_count — число изданий (fb2-файлов) логической книги. >1 → бейдж
  // «N изданий». Заполняется на карточках автора/серии.
  edition_count?: number;
};

// bySeriesOrder — компаратор книг внутри одной серии: по series_order (бэкенд-
// каскад), nil в конец; финальный тайбрейк — название (ru-локаль). Бэкенд уже
// отдаёт книги сортированными, но клиентская сортировка делает группировку на
// странице автора устойчивой к порядку массива.
export function bySeriesOrder(a: BookListItem, b: BookListItem): number {
  const ao = a.series_order ?? Number.POSITIVE_INFINITY;
  const bo = b.series_order ?? Number.POSITIVE_INFINITY;
  if (ao !== bo) return ao - bo;
  return a.title.localeCompare(b.title, 'ru');
}

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

/** EditionRef — одно физическое издание (fb2-файл) логической книги. */
export type EditionRef = {
  id: number;
  lang?: string;
  translator?: string;
  edition_year?: number;
  publisher?: string;
  isbn?: string;
  edition_title?: string;
  page_count?: number;
  cover_path?: string;
  size_bytes: number;
  ext: string;
  archive: string;
  file_name: string;
};

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
  /** Год написания / первого издания произведения (fb2 title-info/date →
   *  внешние источники). Питает гистограмму на страницах автора/серии. */
  written_year?: number;
  /** Год конкретного бумажного издания этого fb2 (publish-info/year).
   *  Справочное поле, в статистику не идёт. */
  edition_year?: number;
  rating?: number;
  annotation?: string;
  cover_path?: string;
  archive: string;
  file_name: string;
  ext: string;
  size_bytes: number;
  deleted?: boolean;
  is_favorite?: boolean;
  is_read?: boolean;
  /** Логическая книга (works.id). */
  work_id?: number;
  /** Все издания этой работы (открытое — первым). Title/written_year/series/
   *  authors/genres выше — уровня работы; lang/cover/archive/file/size — открытого
   *  издания (id выше). На singleton-работе массив из одного элемента. */
  editions?: EditionRef[];
  /** Когда пользователь явно отметил прочитанной (или ридер auto-mark'нул). */
  read_at?: string;
  /** Прогресс чтения [0, 1] из in-browser ридера. Undefined если ридер
   *  ни разу не открывали — UI тогда показывает «Читать» без процента. */
  reading_fraction?: number;
};

function buildBooksParams(opts: BookFilters, limit: number, offset: number): string {
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
  return params.toString();
}

/**
 * useInfiniteBooks — список/поиск книг бесконечной прокруткой.
 *
 * Вместо страничной пагинации (offset в URL) подгружаем следующие
 * страницы по мере скролла (BooksPage вешает IntersectionObserver на
 * sentinel + кнопку «Показать ещё»). pageParam — offset; следующая
 * страница есть пока offset+limit < total.
 *
 * placeholderData: keepPreviousData — при смене фильтра/поиска старый
 * список виден пока подъезжает новый (не моргаем в скелетон). queryKey
 * включает все параметры → смена фильтра начинает infinite-набор заново
 * с offset 0.
 *
 * facets/total берём из первой страницы (они про весь запрос, одинаковы
 * на всех страницах).
 */
export function useInfiniteBooks(opts: Omit<BookFilters, 'offset'>) {
  const limit = opts.limit ?? 20;
  return useInfiniteQuery({
    queryKey: ['books-infinite', { ...opts, limit }],
    initialPageParam: 0,
    queryFn: ({ pageParam, signal }) =>
      apiFetch<BookListResponse>(`/api/books?${buildBooksParams(opts, limit, pageParam)}`, {
        signal,
      }),
    getNextPageParam: (lastPage) => {
      const next = lastPage.offset + lastPage.limit;
      return next < lastPage.total ? next : undefined;
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
 *
 * Возвращает обычный useQuery-результат плюс enrichmentExhausted:
 * флаг "polling исчерпал ретраи, дальше ждать бесполезно". UI
 * использует его чтобы превратить вечный скелетон в "Описание
 * отсутствует" для книг которых нет ни в одном источнике.
 */
const ENRICH_MAX_TRIES = 10;

export function useBook(id: number | string | undefined) {
  const qc = useQueryClient();
  const queryKey = ['book', String(id)] as const;

  const query = useQuery<Book>({
    queryKey: [...queryKey],
    queryFn: ({ signal }) => apiFetch<Book>(`/api/books/${id}`, { signal }),
    enabled: id !== undefined && id !== '',
    refetchInterval: (q) => {
      const data = q.state.data as Book | undefined;
      // Поллим пока хотя бы один артефакт enrichment'а не пришёл:
      // обложка ИЛИ аннотация. Когда оба на месте — успокаиваемся.
      // Если книга в принципе без обложки И без аннотации (бывает) —
      // ограничение по числу ретраев освобождает поллинг через ~20с.
      const haveCover = !!data?.cover_path;
      const haveAnnotation = !!data?.annotation;
      if (haveCover && haveAnnotation) return false;
      if (q.state.dataUpdateCount > ENRICH_MAX_TRIES) return false;
      return 2_000;
    },
  });

  // dataUpdateCount не экспортируется в useQuery-result, читаем из
  // QueryClient'а. После того как refetchInterval вернёт false по
  // ретраям, polling остановится — но dataUpdateCount уже будет > MAX,
  // что мы и используем как сигнал "обогащение завершилось без удачи".
  const state = qc.getQueryState<Book>([...queryKey]);
  const enrichmentExhausted =
    !!state?.data &&
    state.dataUpdateCount > ENRICH_MAX_TRIES &&
    (!state.data.cover_path || !state.data.annotation);

  return { ...query, enrichmentExhausted };
}

/**
 * useToggleRead — переключает is_read у книги через POST/DELETE
 * /api/books/{id}/read. После успеха патчит кэш конкретной книги
 * (cache hit на BookDetailPage сразу обновляется без рефетча).
 *
 * Использует optimistic update: меняем флаг до ответа сервера, при
 * ошибке откатываем. Это нужно потому что кнопка моментально визуально
 * переключается, а сервер отвечает за ~10ms — без optimistic был бы
 * заметный лаг.
 */
export function useToggleRead() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ bookId, isRead }: { bookId: number; isRead: boolean }) =>
      apiFetch<{ is_read: boolean }>(`/api/books/${bookId}/read`, {
        method: isRead ? 'POST' : 'DELETE',
      }),
    onMutate: async ({ bookId, isRead }) => {
      const key = ['book', String(bookId)];
      await qc.cancelQueries({ queryKey: key });
      const prev = qc.getQueryData<Book>(key);
      if (prev) {
        qc.setQueryData<Book>(key, { ...prev, is_read: isRead });
      }
      return { prev, key };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev) {
        qc.setQueryData(ctx.key, ctx.prev);
      }
    },
    onSettled: (_data, _err, { bookId }) => {
      // Инвалидируем book-кэш чтобы из ответа пришли поля которые
      // optimistic update не мог знать: read_at (бэкенд проставляет
      // now() при MarkRead, мы клиентски это значение не имеем). Без
      // этого «Прочитана 17 мая 2026» не появится пока юзер сам не
      // рефрешнет страницу.
      qc.invalidateQueries({ queryKey: ['book', String(bookId)] });
      // Вкладка автора/серии — для read_count в статистике.
      qc.invalidateQueries({ queryKey: ['author'] });
      qc.invalidateQueries({ queryKey: ['series'] });
    },
  });
}

/**
 * useReadingPosition — получить сохранённую позицию чтения (epub-cfi).
 * Возвращает пустую строку если позиции не было — ридер открывает с
 * начала.
 *
 * staleTime небольшой: на reader-странице это запрос «один раз на старте»,
 * после старта позицию обновляет уже сам ридер локально + PUT'ом.
 */
export function useReadingPosition(bookId: number | string | undefined) {
  return useQuery<{ pos: string }>({
    queryKey: ['book', String(bookId), 'position'],
    queryFn: ({ signal }) =>
      apiFetch<{ pos: string }>(`/api/books/${bookId}/position`, { signal }),
    enabled: bookId !== undefined && bookId !== '',
    staleTime: 1_000,
  });
}

/**
 * useSavePosition — мутация для сохранения позиции в БД. Caller
 * (ReaderPage) дёргает её debounce'нуто (раз в 3 секунды максимум)
 * на каждый foliate-js `relocate` event.
 *
 * fraction опциональный — foliate-js обычно его даёт, но в edge-cases
 * (fixed-layout epub'ы) может прислать null. Backend в этом случае
 * не перетирает прежнее значение (см. SavePosition в history service).
 */
export function useSavePosition() {
  return useMutation({
    mutationFn: ({ bookId, pos, fraction }: { bookId: number; pos: string; fraction?: number }) =>
      apiFetch<{ pos: string; fraction?: number }>(`/api/books/${bookId}/position`, {
        method: 'PUT',
        body: fraction !== undefined ? { pos, fraction } : { pos },
      }),
  });
}
