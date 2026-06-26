import { useMemo, useState } from 'react';
import { Check, Pencil, RotateCcw } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import {
  Command,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command';
import { useMe } from '@/lib/auth';
import { useGenres, type GenreItem } from '@/lib/genres';
import { useSetOverride, useRevertOverride } from '@/lib/admin';
import { useLongPress } from '@/lib/useLongPress';
import { type GenreRef } from '@/lib/books';
import { cn } from '@/lib/utils';

/**
 * GenresEditor — жанры работы с правкой для админа (оверрайд genres, грабля №19).
 * Не-админ: обычные чипы (как было). Админ: чипы + правка — десктоп: карандаш на
 * ховере; мобила: лонг-тап по строке. Открывается поповер с ПОИСКОМ и мультиселектом
 * по справочнику жанров; «Сохранить» материализует набор на все издания работы
 * (union = набор). ↺ откат, если оверрайднуто. Визуально незаметно по умолчанию.
 */
export function GenresEditor({
  workId,
  genres,
  overridden = false,
}: {
  workId: number;
  genres: GenreRef[];
  overridden?: boolean;
}) {
  const me = useMe();
  if (me.data?.role !== 'admin') {
    if (genres.length === 0) return null;
    return (
      <div className="flex flex-wrap gap-1">
        {genres.map((g) => (
          <Badge key={g.id} variant="secondary" className="font-normal">
            {g.display}
          </Badge>
        ))}
      </div>
    );
  }
  return <AdminGenres workId={workId} genres={genres} overridden={overridden} />;
}

function AdminGenres({
  workId,
  genres,
  overridden,
}: {
  workId: number;
  genres: GenreRef[];
  overridden: boolean;
}) {
  const allGenres = useGenres();
  const data = allGenres.data;
  const setOverride = useSetOverride();
  const revert = useRevertOverride();
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const [draft, setDraft] = useState<Set<string>>(new Set());
  const longPress = useLongPress(() => startEdit());
  const q = search.trim().toLowerCase();

  // Гибрид: пустой поиск → группировка по категориям (browse); активный поиск →
  // плоский список с двухстрочными результатами (жанр + категория-подпись), т.к.
  // поперёк категорий группировка теряет смысл, а подпись возвращает контекст.
  const grouped = useMemo(() => {
    const m = new Map<string, GenreItem[]>();
    for (const g of data ?? []) {
      const cat = g.category_name || 'Прочее';
      const arr = m.get(cat);
      if (arr) arr.push(g);
      else m.set(cat, [g]);
    }
    return [...m.entries()]
      .map(([cat, items]) => ({
        cat,
        items: [...items].sort((a, b) => a.display.localeCompare(b.display, 'ru')),
      }))
      .sort((a, b) => a.cat.localeCompare(b.cat, 'ru'));
  }, [data]);
  const filtered = useMemo(() => {
    if (q === '') return [];
    return (data ?? [])
      .filter(
        (g) =>
          g.display.toLowerCase().includes(q) ||
          g.code.toLowerCase().includes(q) ||
          (g.category_name ?? '').toLowerCase().includes(q),
      )
      .sort((a, b) => a.display.localeCompare(b.display, 'ru'));
  }, [data, q]);

  function startEdit() {
    setDraft(new Set(genres.map((g) => g.code)));
    setSearch('');
    setOpen(true);
  }
  function toggle(code: string) {
    setDraft((prev) => {
      const next = new Set(prev);
      if (next.has(code)) next.delete(code);
      else next.add(code);
      return next;
    });
  }
  function save() {
    setOverride.mutate(
      { target_kind: 'work', target_id: workId, field: 'genres', value: { codes: [...draft] } },
      { onSuccess: () => setOpen(false) },
    );
  }

  return (
    <div className="group/genres flex flex-wrap items-center gap-1" {...longPress}>
      {genres.length > 0 ? (
        genres.map((g) => (
          <Badge key={g.id} variant="secondary" className="font-normal">
            {g.display}
          </Badge>
        ))
      ) : (
        <span className="text-xs italic text-muted-foreground">жанры не указаны</span>
      )}
      <Popover open={open} onOpenChange={(o) => (o ? startEdit() : setOpen(false))}>
        <PopoverTrigger asChild>
          <button
            type="button"
            aria-label="Изменить жанры"
            className="shrink-0 rounded p-0.5 text-muted-foreground opacity-0 transition-opacity hover:text-foreground focus:opacity-100 group-hover/genres:opacity-100"
          >
            <Pencil className="size-3.5" aria-hidden />
          </button>
        </PopoverTrigger>
        <PopoverContent align="start" className="w-72 p-0">
          <Command shouldFilter={false}>
            <CommandInput placeholder="Поиск жанра…" value={search} onValueChange={setSearch} />
            <CommandList>
              {q === '' ? (
                // browse: группы-категории, в строке только имя жанра.
                grouped.map((grp) => (
                  <CommandGroup key={grp.cat} heading={grp.cat}>
                    {grp.items.map((g) => (
                      <CommandItem key={g.code} value={g.code} onSelect={() => toggle(g.code)}>
                        <Check
                          className={cn('mr-2 size-4 shrink-0', draft.has(g.code) ? 'opacity-100' : 'opacity-0')}
                          aria-hidden
                        />
                        <span className="flex-1 truncate">{g.display}</span>
                      </CommandItem>
                    ))}
                  </CommandGroup>
                ))
              ) : filtered.length > 0 ? (
                // search: плоский список, две строки (жанр + категория-подпись).
                <CommandGroup>
                  {filtered.map((g) => (
                    <CommandItem key={g.code} value={g.code} onSelect={() => toggle(g.code)}>
                      <Check
                        className={cn('mr-2 size-4 shrink-0', draft.has(g.code) ? 'opacity-100' : 'opacity-0')}
                        aria-hidden
                      />
                      <div className="min-w-0 flex-1">
                        <div className="truncate">{g.display}</div>
                        {g.category_name ? (
                          <div className="truncate text-xs text-muted-foreground">{g.category_name}</div>
                        ) : null}
                      </div>
                    </CommandItem>
                  ))}
                </CommandGroup>
              ) : (
                <div className="py-6 text-center text-sm text-muted-foreground">Ничего не найдено</div>
              )}
            </CommandList>
          </Command>
          <div className="flex items-center justify-between gap-2 border-t p-2">
            <span className="text-xs text-muted-foreground">{draft.size} выбрано</span>
            <div className="flex gap-1">
              <Button size="sm" variant="ghost" onClick={() => setOpen(false)}>
                Отмена
              </Button>
              <Button size="sm" onClick={save} disabled={setOverride.isPending}>
                Сохранить
              </Button>
            </div>
          </div>
        </PopoverContent>
      </Popover>
      {overridden ? (
        <button
          type="button"
          onClick={() => revert.mutate({ target_kind: 'work', target_id: workId, field: 'genres' })}
          disabled={revert.isPending}
          aria-label="Отменить правку жанров"
          className="shrink-0 rounded p-0.5 text-muted-foreground opacity-60 transition-opacity hover:text-foreground focus:opacity-100 group-hover/genres:opacity-100 disabled:opacity-30"
        >
          <RotateCcw className="size-3" aria-hidden />
        </button>
      ) : null}
    </div>
  );
}
