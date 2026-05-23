import { useQuery } from '@tanstack/react-query';
import { apiFetch } from './api';

/**
 * GenreItem — то что отдаёт GET /api/genres. category_code / category_name
 * пустые если у жанра нет parent (legacy данные без иерархии); в UI
 * такие падают в группу «Прочее».
 */
export type GenreItem = {
  id: number;
  code: string;
  display: string;
  book_count: number;
  category_code?: string;
  category_name?: string;
};

type ListResponse = { items: GenreItem[] };

/**
 * useGenres — справочник всех fb2-жанров с category-info. Сюда же
 * попадают book_count'ы — но они НЕ user-specific и НЕ зависят от
 * текущего фильтра; для динамических counts (с учётом поиска) надо
 * читать `facets.genres` из BookListResponse.
 *
 * staleTime 5 минут: каталог жанров меняется только при добавлении
 * нового INPX-файла с неизвестным fb2-кодом. До тех пор можно держать
 * один longer-lived snapshot и переиспользовать на всех страницах.
 */
export function useGenres() {
  return useQuery<GenreItem[]>({
    queryKey: ['genres'],
    queryFn: async () => {
      const r = await apiFetch<ListResponse>('/api/genres');
      return r.items;
    },
    staleTime: 5 * 60_000,
  });
}

/**
 * useGenreMap — derived helper. Map fb2_code → GenreItem для быстрых
 * lookup'ов в местах где жанр упоминается кодом (chips в карточке книги,
 * ActiveFilterChips и т.п.).
 *
 * Возвращает пустую Map пока запрос не завершился — caller должен
 * сфолбэкнуть на сам fb2_code если ничего не нашёл.
 */
export function useGenreMap(): Map<string, GenreItem> {
  const q = useGenres();
  const items = q.data ?? [];
  // useMemo не нужен — Map создаётся на каждый рендер, но source items
  // стабилен (react-query кэширует). При смене items (TanStack-cache
  // invalidation) Map тоже обновится — что и хотим.
  const out = new Map<string, GenreItem>();
  for (const it of items) out.set(it.code, it);
  return out;
}
