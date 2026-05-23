import { useEffect, useMemo, useState } from 'react';
import { ChevronRight, Minus } from 'lucide-react';
import { useGenres, type GenreItem } from '@/lib/genres';
import { cn } from '@/lib/utils';

/**
 * GroupedGenresFilter — иерархический фильтр жанров для FiltersSidebar.
 *
 * UX:
 *  - 22 категории collapsed по дефолту (≈ список из 22 chevron-строк).
 *  - Tri-state checkbox у категории: none / partial / all leaf'ов выбраны.
 *  - Кликом по чекбоксу категории: toggle всех leaf'ов (если none/partial → select all;
 *    если all → deselect all).
 *  - Категория с хотя бы одним выбранным leaf'ом — auto-expanded при
 *    маунте + на изменении selection. Дальше пользователь может
 *    свернуть руками; при следующем обновлении selection auto-expand
 *    снова раскроет если добавился новый.
 *  - Leaf'ы без parent_id (legacy данные) попадают в группу «Прочее».
 *
 * Counts:
 *  - Leaf — count из facets (динамический, зависит от поиска/фильтров);
 *    если facets нет — null, не показываем число.
 *  - Категория — сумма leaf-counts. Если хоть у одного leaf'а
 *    count===null, показываем сумму известных без «?» суффикса (минор
 *    inaccuracy предпочтительнее визуального шума).
 *
 * URL-state — управляется сверху: selected — array of fb2_codes,
 * onChange отдаёт новый array. URL остаётся плоским
 * `?genres=sf,sf_action,...` без серверной expansion.
 */
export function GroupedGenresFilter({
  selected,
  facets,
  onChange,
}: {
  selected: string[];
  facets?: Record<string, number>;
  onChange: (next: string[]) => void;
}) {
  const genresQ = useGenres();

  // Группируем leaf'ы по category_name. Категория «Прочее» создаётся
  // только если есть leaf'ы без parent (legacy данные); в production
  // после Seed практически все жанры имеют parent_id, эта группа пуста.
  //
  // facets передаётся в сортировку: leaf'ы внутри категории и сами
  // категории упорядочены по effective count (facets[code] ?? book_count)
  // desc — популярные сверху. Это удобнее алфавита: первым видишь
  // «Фантастика (7)», а не «Военное дело (1)».
  const groups = useMemo(
    () => groupByCategory(genresQ.data ?? [], selected, facets),
    [genresQ.data, selected, facets],
  );

  // Какие категории раскрыты. По дефолту — те, в которых хоть один
  // selected leaf. При изменении selection (через ActiveFilterChips
  // например) auto-expand новых; уже-раскрытые не сворачиваем.
  const [expanded, setExpanded] = useState<Set<string>>(() => initialExpanded(groups));
  useEffect(() => {
    setExpanded((prev) => {
      const next = new Set(prev);
      for (const g of groups) {
        if (g.selectedCount > 0) next.add(g.name);
      }
      return next;
    });
  }, [groups]);

  if (genresQ.isLoading) {
    return (
      <div className="space-y-2">
        <div className="text-xs font-medium text-muted-foreground uppercase">Жанры</div>
        <div className="text-xs italic text-muted-foreground">загружается…</div>
      </div>
    );
  }
  if (groups.length === 0) {
    return null;
  }

  function toggleCategory(group: GroupedCategory) {
    // Полный select-all если был none/partial; deselect-all если был all.
    // Защита: не дёргаем onChange если leaf'ов 0 (теоретически group ниже фильтруется).
    const leafCodes = group.leafs.map((l) => l.code);
    const allOn = group.state === 'all';
    if (allOn) {
      onChange(selected.filter((c) => !leafCodes.includes(c)));
    } else {
      // Объединяем без дублей: existing selected ∪ leafCodes.
      const set = new Set(selected);
      for (const c of leafCodes) set.add(c);
      onChange(Array.from(set));
    }
  }

  function toggleLeaf(code: string, checked: boolean) {
    if (checked) {
      onChange([...selected, code]);
    } else {
      onChange(selected.filter((c) => c !== code));
    }
  }

  function toggleExpanded(name: string) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  }

  return (
    <div className="space-y-2">
      <div className="text-xs font-medium text-muted-foreground uppercase">Жанры</div>
      <ul className="space-y-0.5 max-h-96 overflow-y-auto pr-1">
        {groups.map((g) => {
          const isOpen = expanded.has(g.name);
          return (
            <li key={g.name} className="space-y-0.5">
              <CategoryRow
                group={g}
                open={isOpen}
                onToggleExpand={() => toggleExpanded(g.name)}
                onToggleCheck={() => toggleCategory(g)}
              />
              {isOpen ? (
                <ul className="ml-7 space-y-0.5 border-l border-border/50 pl-2">
                  {g.leafs.map((leaf) => (
                    <LeafRow
                      key={leaf.code}
                      leaf={leaf}
                      checked={selected.includes(leaf.code)}
                      onChange={(c) => toggleLeaf(leaf.code, c)}
                      facets={facets}
                    />
                  ))}
                </ul>
              ) : null}
            </li>
          );
        })}
      </ul>
    </div>
  );
}

// ── internal layouts ───────────────────────────────────────────────

type GroupedCategory = {
  name: string;
  leafs: GenreItem[];
  selectedCount: number;
  state: 'none' | 'partial' | 'all';
  // totalCount — sum of effective counts (facets[code] ?? book_count)
  // по всем leaf'ам категории. Используется и для сортировки категорий,
  // и для display'я суммарного counter'а рядом с названием.
  totalCount: number;
};

function CategoryRow({
  group,
  open,
  onToggleExpand,
  onToggleCheck,
}: {
  group: GroupedCategory;
  open: boolean;
  onToggleExpand: () => void;
  onToggleCheck: () => void;
}) {
  // totalCount precomputed в groupByCategory: facets[code] ?? book_count
  // по всем leaf'ам. Один источник правды и сортировка категорий
  // консистентна с тем что показано в counter'е.
  const totalCount = group.totalCount;
  const hasCount = totalCount > 0;

  return (
    <div className="flex items-center gap-1.5 rounded px-1 py-1 hover:bg-accent/30">
      <button
        type="button"
        onClick={onToggleExpand}
        aria-label={open ? 'Свернуть' : 'Развернуть'}
        aria-expanded={open}
        className="size-5 inline-flex items-center justify-center text-muted-foreground hover:text-foreground"
      >
        <ChevronRight
          className={cn('size-4 transition-transform', open ? 'rotate-90' : '')}
          aria-hidden
        />
      </button>
      <TriStateCheckbox state={group.state} onClick={onToggleCheck} />
      <button
        type="button"
        onClick={onToggleExpand}
        className="flex-1 text-left text-sm font-medium truncate"
      >
        {group.name}
      </button>
      {hasCount ? (
        <span className="text-xs tabular-nums text-muted-foreground">{totalCount}</span>
      ) : null}
    </div>
  );
}

function LeafRow({
  leaf,
  checked,
  onChange,
  facets,
}: {
  leaf: GenreItem;
  checked: boolean;
  onChange: (next: boolean) => void;
  facets?: Record<string, number>;
}) {
  const count = facets?.[leaf.code];
  return (
    <li>
      <label className="flex items-center gap-2 cursor-pointer rounded px-1 py-0.5 hover:bg-accent/40">
        <input
          type="checkbox"
          className="size-4 rounded border-input"
          checked={checked}
          onChange={(e) => onChange(e.target.checked)}
        />
        <span className="flex-1 truncate text-sm">{leaf.display}</span>
        {count != null && count > 0 ? (
          <span className="text-xs tabular-nums text-muted-foreground">{count}</span>
        ) : null}
      </label>
    </li>
  );
}

/**
 * TriStateCheckbox — нативный checkbox с indeterminate state установленным
 * через ref (HTML-атрибут indeterminate существует только на DOM-node,
 * не на JSX-props). Доступность: aria-checked = "mixed" для partial.
 *
 * Используем native input а не shadcn Checkbox чтобы не таскать Radix
 * primitives ради одного места — стилистически отличаем от leaf checkboxes
 * другой кнопкой (нет label-обёртки), это OK.
 */
function TriStateCheckbox({
  state,
  onClick,
}: {
  state: 'none' | 'partial' | 'all';
  onClick: () => void;
}) {
  if (state === 'partial') {
    // Кастомный визуал для indeterminate — нативный stylr browser-y
    // выглядит инконсистентно на разных OS. Чёрный квадрат с minus icon.
    return (
      <button
        type="button"
        onClick={onClick}
        role="checkbox"
        aria-checked="mixed"
        aria-label="Выбрана часть жанров категории"
        className="size-4 inline-flex items-center justify-center rounded border border-input bg-primary text-primary-foreground"
      >
        <Minus className="size-3" aria-hidden />
      </button>
    );
  }
  return (
    <input
      type="checkbox"
      checked={state === 'all'}
      onChange={onClick}
      className="size-4 rounded border-input"
      aria-label={state === 'all' ? 'Снять выделение со всех' : 'Выбрать все в категории'}
    />
  );
}

// ── pure helpers ───────────────────────────────────────────────────

const FALLBACK_CATEGORY = 'Прочее';

function groupByCategory(
  items: GenreItem[],
  selected: string[],
  facets?: Record<string, number>,
): GroupedCategory[] {
  const map = new Map<string, GenreItem[]>();
  for (const it of items) {
    // Defensive: skip malformed entries (отсутствие code/display ломает
    // sort и render). Backend гарантирует эти поля непустыми, но если
    // /api/genres ответил мусором (мок в тестах, badpath, race) —
    // лучше отфильтровать чем уронить весь sidebar.
    if (!it || typeof it.code !== 'string' || typeof it.display !== 'string') {
      continue;
    }
    const cat = it.category_name && it.category_name.length > 0
      ? it.category_name
      : FALLBACK_CATEGORY;
    let bucket = map.get(cat);
    if (!bucket) {
      bucket = [];
      map.set(cat, bucket);
    }
    bucket.push(it);
  }
  const selSet = new Set(selected);

  // effectiveCount — динамический (от текущего поиска через facets) если
  // есть, иначе статический total. Используется и для сортировки, и
  // для подсчёта sum'ы у категории.
  const effective = (g: GenreItem) =>
    facets?.[g.code] ?? g.book_count ?? 0;

  const out: GroupedCategory[] = [];
  for (const [name, leafs] of map) {
    // Сортировка leaf'ов: по убыванию count, tiebreaker — display asc
    // для стабильного порядка при равных счётчиках.
    leafs.sort((a, b) => {
      const diff = effective(b) - effective(a);
      if (diff !== 0) return diff;
      return a.display.localeCompare(b.display, 'ru');
    });
    const selectedCount = leafs.filter((l) => selSet.has(l.code)).length;
    let state: GroupedCategory['state'] = 'none';
    if (selectedCount === leafs.length && leafs.length > 0) state = 'all';
    else if (selectedCount > 0) state = 'partial';
    const totalCount = leafs.reduce((acc, l) => acc + effective(l), 0);
    out.push({ name, leafs, selectedCount, state, totalCount });
  }
  // Категории: «Прочее» всегда последняя (fallback-bucket для legacy
  // данных, неважно сколько там книг). Остальные — по убыванию totalCount,
  // tiebreaker — алфавит.
  out.sort((a, b) => {
    if (a.name === FALLBACK_CATEGORY) return 1;
    if (b.name === FALLBACK_CATEGORY) return -1;
    const diff = b.totalCount - a.totalCount;
    if (diff !== 0) return diff;
    return a.name.localeCompare(b.name, 'ru');
  });
  return out;
}

function initialExpanded(groups: GroupedCategory[]): Set<string> {
  const out = new Set<string>();
  for (const g of groups) {
    if (g.selectedCount > 0) out.add(g.name);
  }
  return out;
}
