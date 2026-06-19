import { useMemo, useState } from 'react';
import { useNavigate } from '@tanstack/react-router';
import { Star, Tag } from 'lucide-react';
import { Callout } from '@/components/ui/callout';
import { Input } from '@/components/ui/input';
import { useGenres, useToggleFavoriteGenre, type GenreItem } from '@/lib/genres';
import { cn } from '@/lib/utils';

/**
 * GenresPage — раздел «Жанры» (/genres): обзор стандартных fb2-жанров по
 * категориям со звездой избранного (избранные закреплены сверху). Клик по
 * жанру → список книг этого жанра (/books?genres=[code]).
 *
 * Личные полки вынесены на отдельный маршрут /shelves (доступ из меню юзера) —
 * это личная библиотека, а не каталог-браузинг.
 *
 * Доступно любому залогиненному пользователю (не только админу).
 */
export function GenresPage() {
  return <GenresOverview />;
}

const FALLBACK_CATEGORY = 'Прочее';

type GenreGroup = { name: string; genres: GenreItem[] };

/**
 * GenresOverview — список fb2-жанров. Сверху — закреплённые избранные (плоско,
 * без категорий), ниже — все жанры, сгруппированные по категориям (как в
 * GroupedGenresFilter: category_name → ряд жанров; legacy без parent → «Прочее»).
 */
function GenresOverview() {
  const genresQ = useGenres();
  const [query, setQuery] = useState('');
  const q = query.trim().toLowerCase();

  // useMemo по genresQ.data (стабильная ссылка из react-query), а не по
  // `?? []`-выражению (новый массив каждый рендер ⇒ memo бесполезен).
  const genres = useMemo(() => genresQ.data ?? [], [genresQ.data]);

  const favorites = useMemo(
    () =>
      genres
        .filter((g) => g.is_favorite)
        .sort((a, b) => a.display.localeCompare(b.display, 'ru')),
    [genres],
  );

  const groups = useMemo(() => {
    if (q) {
      // При поиске показываем единый отфильтрованный список (включая избранные —
      // закреплённую секцию при поиске не рисуем, чтобы не дублировать).
      const filtered = genres.filter(
        (g) =>
          (g.display ?? '').toLowerCase().includes(q) ||
          (g.category_name ?? '').toLowerCase().includes(q),
      );
      return groupByCategory(filtered);
    }
    // Без поиска избранные вынесены наверх отдельной секцией — исключаем их из
    // категорий, чтобы один и тот же жанр не показывался дважды.
    return groupByCategory(genres.filter((g) => !g.is_favorite));
  }, [genres, q]);

  return (
    <section className="space-y-3">
      <div className="flex items-center justify-between gap-2">
        <h2 className="flex items-center gap-2 text-lg font-semibold tracking-tight">
          <Tag className="size-5" aria-hidden />
          Жанры
        </h2>
      </div>

      <Input
        type="search"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        placeholder="Поиск жанра…"
        aria-label="Поиск жанра"
        className="h-9 max-w-sm text-sm"
      />

      {genresQ.isLoading ? (
        <p className="text-sm italic text-muted-foreground">Загрузка…</p>
      ) : genres.length === 0 ? (
        <Callout>Справочник жанров пуст.</Callout>
      ) : (
        <div className="space-y-5">
          {/* Закреплённые избранные — только когда нет активного поиска
              (при поиске показываем единый отфильтрованный список). */}
          {!q && favorites.length > 0 ? (
            <div className="space-y-1.5">
              <h3 className="flex items-center gap-1.5 text-xs font-medium uppercase text-muted-foreground">
                <Star className="size-3.5 fill-yellow-500 stroke-yellow-500" aria-hidden />
                Избранные
              </h3>
              <div className="flex flex-wrap gap-2">
                {favorites.map((g) => (
                  <GenreChip key={g.id} genre={g} />
                ))}
              </div>
            </div>
          ) : null}

          {groups.length === 0 ? (
            <p className="px-1 text-sm italic text-muted-foreground">Ничего не найдено.</p>
          ) : (
            groups.map((group) => (
              <div key={group.name} className="space-y-1.5">
                <h3 className="text-xs font-medium uppercase text-muted-foreground">
                  {group.name}
                </h3>
                <div className="flex flex-wrap gap-2">
                  {group.genres.map((g) => (
                    <GenreChip key={g.id} genre={g} />
                  ))}
                </div>
              </div>
            ))
          )}
        </div>
      )}
    </section>
  );
}

/**
 * GenreChip — жанр: кликабельная зона (→ список книг этого жанра) + звезда
 * избранного. Звезда — существующий паттерн (как FavoriteButton), монохром
 * не нарушаем: цвет только у активной звезды (исключение в граблю №9).
 */
function GenreChip({ genre }: { genre: GenreItem }) {
  const navigate = useNavigate();
  const toggle = useToggleFavoriteGenre();
  const isFav = genre.is_favorite ?? false;
  return (
    <span className="inline-flex items-center overflow-hidden rounded-md border border-border bg-background">
      <button
        type="button"
        onClick={() => void navigate({ to: '/books', search: { genres: [genre.code] } })}
        className="px-2.5 py-1 text-sm transition hover:bg-accent/40"
      >
        {genre.display}
        {genre.book_count > 0 ? (
          <span className="ml-1.5 text-xs tabular-nums text-muted-foreground">
            {genre.book_count}
          </span>
        ) : null}
      </button>
      <button
        type="button"
        onClick={() => toggle.mutate({ id: genre.id, next: !isFav })}
        disabled={toggle.isPending}
        aria-pressed={isFav}
        aria-label={isFav ? `Убрать «${genre.display}» из избранного` : `Добавить «${genre.display}» в избранное`}
        className="flex h-full items-center border-l border-border px-1.5 py-1 transition hover:bg-accent/40 disabled:opacity-50"
      >
        <Star
          className={cn(
            'size-3.5',
            isFav ? 'fill-yellow-500 stroke-yellow-500' : 'text-muted-foreground',
          )}
          aria-hidden
        />
      </button>
    </span>
  );
}

function groupByCategory(items: GenreItem[]): GenreGroup[] {
  const map = new Map<string, GenreItem[]>();
  for (const it of items) {
    if (!it || typeof it.code !== 'string' || typeof it.display !== 'string') continue;
    const cat =
      it.category_name && it.category_name.length > 0 ? it.category_name : FALLBACK_CATEGORY;
    const bucket = map.get(cat) ?? [];
    bucket.push(it);
    map.set(cat, bucket);
  }
  const out: GenreGroup[] = [];
  for (const [name, genres] of map) {
    genres.sort((a, b) => {
      const diff = (b.book_count ?? 0) - (a.book_count ?? 0);
      if (diff !== 0) return diff;
      return a.display.localeCompare(b.display, 'ru');
    });
    out.push({ name, genres });
  }
  // «Прочее» последняя; остальные — по суммарному числу книг (популярные сверху),
  // tiebreak — алфавит.
  out.sort((a, b) => {
    if (a.name === FALLBACK_CATEGORY) return 1;
    if (b.name === FALLBACK_CATEGORY) return -1;
    const sum = (g: GenreGroup) => g.genres.reduce((acc, x) => acc + (x.book_count ?? 0), 0);
    const diff = sum(b) - sum(a);
    if (diff !== 0) return diff;
    return a.name.localeCompare(b.name, 'ru');
  });
  return out;
}
