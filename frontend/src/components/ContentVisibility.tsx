import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { ChevronRight, Languages, Lock, Minus, Search, Tags, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { cn } from '@/lib/utils';
import type { GenreItem } from '@/lib/genres';
import type { LanguageItem } from '@/lib/content';

/**
 * Редакторы видимости контента (общие для админки и профиля). Семантика
 * контрола — «Скрыть»: отмечено (крест) ⇒ книги этого языка/жанра не
 * показываются в списках/поиске/фильтрах. Крест (а не галочка) — потому что
 * действие выключает контент, а не включает.
 *
 * locked — жанры/языки, скрытые глобально администратором (только в
 * профиле). Они отображаются отмеченными и заблокированными (Lock-иконка):
 * пользователь не может их «включить обратно», но видит, что они скрыты.
 * onChange меняет ТОЛЬКО собственный набор (hidden), не трогая locked.
 */

function withCode(list: string[], code: string, present: boolean): string[] {
  const set = new Set(list);
  if (present) set.add(code);
  else set.delete(code);
  return Array.from(set);
}

type HideState = 'off' | 'partial' | 'on';

function ariaCheckedValue(state: HideState): boolean | 'mixed' {
  if (state === 'partial') return 'mixed';
  return state === 'on';
}

/**
 * HideBox — визуальный квадрат контрола «Скрыть»: пусто (видно) / крест
 * (скрыто) / минус (часть категории скрыта). Сам не интерактивен — клик
 * вешается на строку-кнопку (листья) или на HideToggle (категории).
 */
function HideBox({ state }: { state: HideState }) {
  const filled = state !== 'off';
  return (
    <span
      aria-hidden
      className={cn(
        'size-4 inline-flex shrink-0 items-center justify-center rounded border border-input',
        filled ? 'bg-primary text-primary-foreground' : 'bg-background',
      )}
    >
      {state === 'on' ? <X className="size-3" /> : null}
      {state === 'partial' ? <Minus className="size-3" /> : null}
    </span>
  );
}

/** HideToggle — интерактивный контрол категории (отдельная кнопка, т.к. в
 *  строке категории рядом ещё кнопка разворота — строку целиком кнопкой не
 *  сделать). Листья используют строку-кнопку + HideBox. */
function HideToggle({
  state,
  label,
  disabled = false,
  onToggle,
}: {
  state: HideState;
  label: string;
  disabled?: boolean;
  onToggle: () => void;
}) {
  return (
    <button
      type="button"
      role="checkbox"
      aria-checked={ariaCheckedValue(state)}
      aria-label={label}
      disabled={disabled}
      onClick={onToggle}
      className={cn('inline-flex shrink-0 rounded', disabled ? 'cursor-not-allowed' : 'cursor-pointer')}
    >
      <HideBox state={state} />
    </button>
  );
}

/**
 * ContentEditor — две карточки («Языки», «Жанры») + footer (кнопка
 * сохранения). Общий для админки и профиля; разница (область действия,
 * locked-набор) приходит через пропсы.
 */
export function ContentEditor({
  languages,
  genres,
  hiddenGenres,
  hiddenLanguages,
  lockedGenres = [],
  lockedLanguages = [],
  onChangeGenres,
  onChangeLanguages,
  footer,
}: {
  languages: LanguageItem[];
  genres: GenreItem[];
  hiddenGenres: string[];
  hiddenLanguages: string[];
  lockedGenres?: string[];
  lockedLanguages?: string[];
  onChangeGenres: (next: string[]) => void;
  onChangeLanguages: (next: string[]) => void;
  footer?: ReactNode;
}) {
  return (
    <>
      <Card>
        <CardContent className="space-y-3 sm:max-w-md">
          <h2 className="flex items-center gap-2 text-base font-semibold">
            <Languages className="size-4" aria-hidden />
            Языки
          </h2>
          <LanguageVisibilityList
            languages={languages}
            hidden={hiddenLanguages}
            locked={lockedLanguages}
            onChange={onChangeLanguages}
          />
        </CardContent>
      </Card>

      <Card>
        <CardContent className="space-y-3 sm:max-w-md">
          <h2 className="flex items-center gap-2 text-base font-semibold">
            <Tags className="size-4" aria-hidden />
            Жанры
          </h2>
          <GenreVisibilityList
            genres={genres}
            hidden={hiddenGenres}
            locked={lockedGenres}
            onChange={onChangeGenres}
          />
        </CardContent>
      </Card>

      {footer}
    </>
  );
}

// LOCKED_HINT — текст тултипа для пунктов, скрытых администратором.
const LOCKED_HINT = 'Скрыто администратором — изменить нельзя';

/**
 * HideRow — одиночная строка «Скрыть …» вне списков (профильная настройка
 * «Скрывать сборники»): та же семантика креста и тач-зона, что у листьев
 * языков/жанров, но без счётчика и locked-состояния.
 */
export function HideRow({
  label,
  hidden,
  onToggle,
}: {
  label: string;
  hidden: boolean;
  onToggle: () => void;
}) {
  return (
    <button
      type="button"
      role="checkbox"
      aria-checked={hidden}
      aria-label={label}
      onClick={onToggle}
      className="flex min-h-9 w-full cursor-pointer items-center gap-2.5 rounded px-2 py-2 text-left hover:bg-accent/40"
    >
      <HideBox state={hidden ? 'on' : 'off'} />
      <span className="flex-1 truncate text-sm">{label}</span>
    </button>
  );
}

// ── Языки ───────────────────────────────────────────────────────────

export function LanguageVisibilityList({
  languages,
  hidden,
  locked = [],
  onChange,
}: {
  languages: LanguageItem[];
  hidden: string[];
  locked?: string[];
  onChange: (next: string[]) => void;
}) {
  const lockedSet = new Set(locked);
  const hiddenSet = new Set(hidden);

  if (languages.length === 0) {
    return <p className="text-sm italic text-muted-foreground">Языки не найдены.</p>;
  }

  // Массовые действия (учитывают locked — их не трогаем): языков на проде
  // несколько десятков, по одному скрывать неудобно.
  const toggleable = languages.map((l) => l.code).filter((c) => !lockedSet.has(c));
  const allHidden = toggleable.length > 0 && toggleable.every((c) => hiddenSet.has(c));
  const noneHidden = toggleable.every((c) => !hiddenSet.has(c));
  const hideAll = () => onChange(Array.from(new Set([...hidden, ...toggleable])));
  const showAll = () => onChange(hidden.filter((c) => !toggleable.includes(c)));

  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs text-muted-foreground">
          {languages.length} {pluralLang(languages.length)}
        </span>
        <div className="flex gap-1">
          <Button
            variant="ghost"
            size="sm"
            className="h-7 px-2 text-xs"
            onClick={hideAll}
            disabled={allHidden}
          >
            Скрыть все
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="h-7 px-2 text-xs"
            onClick={showAll}
            disabled={noneHidden}
          >
            Показать все
          </Button>
        </div>
      </div>
      <ul aria-label="Языки">
        {languages.map((l) => {
          const isLocked = lockedSet.has(l.code);
          const checked = isLocked || hiddenSet.has(l.code);
          return (
            <li key={l.code} title={isLocked ? LOCKED_HINT : undefined}>
              <button
                type="button"
                role="checkbox"
                aria-checked={checked}
                aria-label={`Скрыть язык: ${l.display}`}
                disabled={isLocked}
                onClick={() => onChange(withCode(hidden, l.code, !checked))}
                className={cn(
                  // Крупная тач-зона (min-h-9, px-2 py-2) — на телефоне легко
                  // промахнуться мимо маленького чекбокса.
                  'flex min-h-9 w-full items-center gap-2.5 rounded px-2 py-2 text-left',
                  isLocked ? 'cursor-not-allowed opacity-60' : 'cursor-pointer hover:bg-accent/40',
                )}
              >
                <HideBox state={checked ? 'on' : 'off'} />
                <span className="flex-1 truncate text-sm">{l.display}</span>
                {isLocked ? (
                  <Lock className="size-3.5 text-muted-foreground" aria-label="скрыто администратором" />
                ) : null}
                <span className="text-xs tabular-nums text-muted-foreground">{l.book_count}</span>
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

// pluralLang — «1 язык / 2 языка / 5 языков».
function pluralLang(n: number): string {
  const m10 = n % 10;
  const m100 = n % 100;
  if (m100 >= 11 && m100 <= 14) return 'языков';
  if (m10 === 1) return 'язык';
  if (m10 >= 2 && m10 <= 4) return 'языка';
  return 'языков';
}

// ── Жанры (сгруппированы по категориям, с поиском) ──────────────────

const FALLBACK_CATEGORY = 'Прочее';

type GenreGroup = { name: string; leafs: GenreItem[] };

function groupGenres(items: GenreItem[]): GenreGroup[] {
  const map = new Map<string, GenreItem[]>();
  for (const it of items) {
    if (!it || typeof it.code !== 'string' || typeof it.display !== 'string') continue;
    const cat = it.category_name && it.category_name.length > 0 ? it.category_name : FALLBACK_CATEGORY;
    let bucket = map.get(cat);
    if (!bucket) {
      bucket = [];
      map.set(cat, bucket);
    }
    bucket.push(it);
  }
  const out: GenreGroup[] = [];
  for (const [name, leafs] of map) {
    leafs.sort((a, b) => a.display.localeCompare(b.display, 'ru'));
    out.push({ name, leafs });
  }
  out.sort((a, b) => {
    if (a.name === FALLBACK_CATEGORY) return 1;
    if (b.name === FALLBACK_CATEGORY) return -1;
    return a.name.localeCompare(b.name, 'ru');
  });
  return out;
}

export function GenreVisibilityList({
  genres,
  hidden,
  locked = [],
  onChange,
}: {
  genres: GenreItem[];
  hidden: string[];
  locked?: string[];
  onChange: (next: string[]) => void;
}) {
  const [query, setQuery] = useState('');
  const q = query.trim().toLowerCase();
  const lockedSet = useMemo(() => new Set(locked), [locked]);
  const hiddenSet = useMemo(() => new Set(hidden), [hidden]);

  const groups = useMemo(() => {
    const items = q
      ? genres.filter(
          (it) =>
            (it.display ?? '').toLowerCase().includes(q) ||
            (it.category_name ?? '').toLowerCase().includes(q),
        )
      : genres;
    return groupGenres(items);
  }, [genres, q]);

  // По дефолту категории свёрнуты; при поиске — все раскрыты.
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  useEffect(() => {
    // Категории, где есть скрытые (свои или admin) — авто-раскрываем,
    // чтобы пользователь видел текущее состояние. Если добавлять нечего —
    // возвращаем prev (тот же ref) → React не ререндерит, эффект не зациклится
    // даже если родитель передаёт новые массивы каждый рендер.
    setExpanded((prev) => {
      let changed = false;
      const next = new Set(prev);
      for (const g of groups) {
        if (!next.has(g.name) && g.leafs.some((l) => hiddenSet.has(l.code) || lockedSet.has(l.code))) {
          next.add(g.name);
          changed = true;
        }
      }
      return changed ? next : prev;
    });
  }, [groups, hiddenSet, lockedSet]);

  if (genres.length === 0) {
    return <p className="text-sm italic text-muted-foreground">Жанры не найдены.</p>;
  }

  // Скрыть/показать все НЕ-locked жанры категории.
  function toggleCategory(group: GenreGroup, hideAll: boolean) {
    const set = new Set(hidden);
    for (const leaf of group.leafs) {
      if (lockedSet.has(leaf.code)) continue;
      if (hideAll) set.add(leaf.code);
      else set.delete(leaf.code);
    }
    onChange(Array.from(set));
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
    <div className="space-y-2" aria-label="Жанры">
      <div className="relative">
        <Search
          className="absolute left-2 top-1/2 -translate-y-1/2 size-3.5 text-muted-foreground"
          aria-hidden
        />
        <Input
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Поиск жанра…"
          aria-label="Поиск жанра"
          className="h-8 pl-7 text-sm"
        />
      </div>
      {groups.length === 0 ? (
        <div className="px-1 py-2 text-xs italic text-muted-foreground">Ничего не найдено</div>
      ) : (
        <ul className="space-y-0.5 max-h-[28rem] overflow-y-auto pr-1">
          {groups.map((g) => {
            const isOpen = q !== '' || expanded.has(g.name);
            const hiddenCount = g.leafs.filter((l) => hiddenSet.has(l.code) || lockedSet.has(l.code)).length;
            const state: HideState =
              hiddenCount === 0 ? 'off' : hiddenCount === g.leafs.length ? 'on' : 'partial';
            // Категория целиком скрыта администратором — все её жанры locked.
            // Тогда строка категории тоже locked: замок, приглушена,
            // переключатель заблокирован (нечего включать — всё admin).
            const allLocked = g.leafs.length > 0 && g.leafs.every((l) => lockedSet.has(l.code));
            const categoryCount = g.leafs.reduce((acc, l) => acc + (l.book_count ?? 0), 0);
            return (
              <li key={g.name} className="space-y-0.5">
                <div
                  title={allLocked ? LOCKED_HINT : undefined}
                  className={cn(
                    'flex items-center gap-1.5 rounded px-1 py-1 hover:bg-accent/30',
                    allLocked && 'opacity-60',
                  )}
                >
                  <button
                    type="button"
                    onClick={() => toggleExpanded(g.name)}
                    aria-label={isOpen ? 'Свернуть' : 'Развернуть'}
                    aria-expanded={isOpen}
                    className="size-5 inline-flex items-center justify-center text-muted-foreground hover:text-foreground"
                  >
                    <ChevronRight
                      className={cn('size-4 transition-transform', isOpen ? 'rotate-90' : '')}
                      aria-hidden
                    />
                  </button>
                  <HideToggle
                    state={state}
                    disabled={allLocked}
                    label={
                      allLocked
                        ? `Категория «${g.name}» скрыта администратором`
                        : state === 'on'
                          ? `Показать все жанры «${g.name}»`
                          : `Скрыть все жанры «${g.name}»`
                    }
                    onToggle={() => toggleCategory(g, state !== 'on')}
                  />
                  <button
                    type="button"
                    onClick={() => toggleExpanded(g.name)}
                    className="flex-1 text-left text-sm font-medium truncate"
                  >
                    {g.name}
                  </button>
                  {allLocked ? (
                    <Lock className="size-3.5 text-muted-foreground" aria-label="скрыто администратором" />
                  ) : null}
                  {categoryCount > 0 ? (
                    <span className="text-xs tabular-nums text-muted-foreground">{categoryCount}</span>
                  ) : null}
                </div>
                {isOpen ? (
                  <ul className="ml-7 space-y-0.5 border-l border-border/50 pl-2">
                    {g.leafs.map((leaf) => {
                      const isLocked = lockedSet.has(leaf.code);
                      const checked = isLocked || hiddenSet.has(leaf.code);
                      return (
                        <li key={leaf.code} title={isLocked ? LOCKED_HINT : undefined}>
                          <button
                            type="button"
                            role="checkbox"
                            aria-checked={checked}
                            aria-label={`Скрыть жанр: ${leaf.display}`}
                            disabled={isLocked}
                            onClick={() => onChange(withCode(hidden, leaf.code, !checked))}
                            className={cn(
                              'flex w-full items-center gap-2 rounded px-1 py-0.5 text-left',
                              isLocked ? 'cursor-not-allowed opacity-60' : 'cursor-pointer hover:bg-accent/40',
                            )}
                          >
                            <HideBox state={checked ? 'on' : 'off'} />
                            <span className="flex-1 truncate text-sm">{leaf.display}</span>
                            {isLocked ? (
                              <Lock
                                className="size-3.5 text-muted-foreground"
                                aria-label="скрыто администратором"
                              />
                            ) : null}
                            {leaf.book_count > 0 ? (
                              <span className="text-xs tabular-nums text-muted-foreground">
                                {leaf.book_count}
                              </span>
                            ) : null}
                          </button>
                        </li>
                      );
                    })}
                  </ul>
                ) : null}
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
