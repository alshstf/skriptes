import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from './api';

/**
 * GenreItem — то что отдаёт GET /api/genres. category_code / category_name
 * пустые если у жанра нет parent (legacy данные без иерархии); в UI
 * такие падают в группу «Прочее».
 *
 * is_favorite — жанр в личном избранном текущего пользователя (раздел «Жанры»,
 * закрепление сверху). false для анонима/OPDS.
 */
export type GenreItem = {
  id: number;
  code: string;
  display: string;
  book_count: number;
  category_code?: string;
  category_name?: string;
  is_favorite?: boolean;
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

/**
 * useToggleFavoriteGenre — переключить жанр в избранном. Оптимистично пишет
 * is_favorite в кэш ['genres'] (звезда переключается мгновенно), на ошибке —
 * откат. Жанры — не сигнал персонализации (широкая категория), поэтому
 * списки книг/поиск НЕ инвалидируем (в отличие от useToggleFavorite).
 *
 * Без blanket-invalidate на onSettled (как useToggleRead в books.ts): /api/genres
 * — тяжёлый запрос (сотни строк с per-genre count-подзапросами), а оптимистичный
 * патч и так держит кэш в актуальном состоянии; откат на ошибке закрывает
 * рассинхрон. Лишний рефетч всего справочника на каждый клик звезды не нужен.
 */
export function useToggleFavoriteGenre() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (vars: { id: number; next: boolean }) => {
      await apiFetch(`/api/genres/${vars.id}/favorite`, {
        method: vars.next ? 'POST' : 'DELETE',
      });
      return vars.next;
    },
    onMutate: async ({ id, next }) => {
      await qc.cancelQueries({ queryKey: ['genres'] });
      const prev = qc.getQueryData<GenreItem[]>(['genres']);
      if (prev) {
        qc.setQueryData<GenreItem[]>(
          ['genres'],
          prev.map((g) => (g.id === id ? { ...g, is_favorite: next } : g)),
        );
      }
      return { prev };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev) qc.setQueryData(['genres'], ctx.prev);
    },
  });
}

/**
 * collapseGenreChips — схлопывает полностью выбранные категории жанров в
 * агрегированные чипы. Категория «полная», когда выбраны ВСЕ её leaf-жанры
 * (и их ≥2 — одиночный жанр остаётся обычным чипом). rest — выбранные коды,
 * не вошедшие в полные категории, в исходном порядке. Пустой справочник
 * (запрос в полёте) → всё в rest (фолбэк на прежнее поведение). Чистая
 * функция — тестируется без рендера.
 */
export function collapseGenreChips(
  selected: string[],
  allGenres: GenreItem[],
): { fullCategories: { name: string; codes: string[] }[]; rest: string[] } {
  const byCategory = new Map<string, string[]>();
  for (const g of allGenres) {
    const cat = g.category_name ?? '';
    if (!cat) continue;
    const bucket = byCategory.get(cat);
    if (bucket) {
      bucket.push(g.code);
    } else {
      byCategory.set(cat, [g.code]);
    }
  }
  const selSet = new Set(selected);
  const consumed = new Set<string>();
  const fullCategories: { name: string; codes: string[] }[] = [];
  for (const [name, codes] of byCategory) {
    if (codes.length < 2 || !codes.every((c) => selSet.has(c))) continue;
    fullCategories.push({ name, codes });
    for (const c of codes) consumed.add(c);
  }
  return { fullCategories, rest: selected.filter((c) => !consumed.has(c)) };
}
