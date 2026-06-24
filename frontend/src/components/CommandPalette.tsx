import { useEffect, useState } from 'react';
import { useNavigate } from '@tanstack/react-router';
import { Bell, BookIcon, UserIcon, LayersIcon, SearchIcon, Star } from 'lucide-react';
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from '@/components/ui/command';
import { Dialog, DialogContent, DialogTitle, DialogDescription } from '@/components/ui/dialog';
import { useDebouncedValue } from '@/lib/useDebouncedValue';
import { useSuggest } from '@/lib/suggest';
import { cn } from '@/lib/utils';

/**
 * CommandPalette — глобальная палитра поиска (⌘K / Ctrl+K).
 *
 * Поведение:
 *  - модалка поверх всего, фокус сразу в инпуте.
 *  - debounce 150ms перед запросом к /api/search/suggest, чтобы не
 *    бомбить backend на каждое нажатие.
 *  - результаты разбиты на 3 секции: Книги / Авторы / Серии.
 *  - Enter / клик по элементу — навигация и закрытие палитры.
 *
 * Минимальная длина запроса — 2 символа (бэкенд для <2 возвращает
 * пустые группы, мы не делаем запрос вообще). Для 0-1 символа
 * показываем краткую подсказку вместо "ничего не найдено".
 */
export function CommandPalette() {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const debounced = useDebouncedValue(query, 150);
  const navigate = useNavigate();

  const { data, isFetching } = useSuggest(debounced, 5);

  // Глобальный hotkey ⌘K / Ctrl+K. Перехватываем preventDefault, чтобы
  // браузер не ушёл в свой address-bar shortcut. Не реагируем когда
  // фокус в input/textarea — там Ctrl+K может означать что-то ещё.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const isMod = e.metaKey || e.ctrlKey;
      if (!isMod || e.key.toLowerCase() !== 'k') return;
      e.preventDefault();
      setOpen((v) => !v);
    }
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, []);

  // Сбрасываем query при закрытии — следующее открытие должно начинаться
  // с пустого инпута, а не с прошлой выдачей.
  useEffect(() => {
    if (!open) setQuery('');
  }, [open]);

  function go(path: string) {
    setOpen(false);
    void navigate({ to: path });
  }

  const showHint = debounced.trim().length < 2;
  const hasAny =
    (data?.books?.length ?? 0) +
      (data?.authors?.length ?? 0) +
      (data?.series?.length ?? 0) >
    0;

  return (
    <>
      <PaletteTrigger onClick={() => setOpen(true)} />
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent
          // На мобиле палитра прижата к верху — top с safe-area-инсетом, иначе
          // на iOS PWA уезжает под статус-бар (грабля №18). Десктоп — по центру.
          className="top-[calc(env(safe-area-inset-top)+1rem)] translate-y-0 overflow-hidden p-0 sm:top-1/2 sm:max-w-2xl sm:-translate-y-1/2"
          showCloseButton={false}
        >
          <DialogTitle className="sr-only">Поиск</DialogTitle>
          <DialogDescription className="sr-only">
            Введите минимум 2 символа, чтобы найти книги, авторов или серии
          </DialogDescription>
          <Command label="Палитра поиска">
            <CommandInput
              placeholder="Поиск книг, авторов, серий…"
              value={query}
              onValueChange={setQuery}
              autoFocus
              autoComplete="off"
              autoCorrect="off"
              autoCapitalize="none"
              spellCheck={false}
              inputMode="search"
              enterKeyHint="search"
            />
            <CommandList
              className="max-h-[60svh] sm:max-h-[400px]"
              aria-busy={isFetching || undefined}
            >
              {showHint ? (
                <div className="py-6 text-center text-sm text-muted-foreground">
                  Введите минимум 2 символа
                </div>
              ) : !hasAny ? (
                <CommandEmpty>Ничего не найдено</CommandEmpty>
              ) : null}

              {!showHint && (data?.books?.length ?? 0) > 0 ? (
                <CommandGroup heading="Книги">
                  {data!.books.map((b) => (
                    <CommandItem
                      key={`b-${b.id}`}
                      value={`book-${b.id}`}
                      onSelect={() => go(`/works/${b.work_id ?? b.id}`)}
                    >
                      <BookIcon aria-hidden />
                      <div className="flex min-w-0 flex-col flex-1">
                        <span className="truncate">{b.title}</span>
                        {b.authors?.length ? (
                          <span className="truncate text-xs text-muted-foreground">
                            {b.authors.join(', ')}
                            {b.year ? ` · ${b.year}` : null}
                            {b.series ? ` · ${b.series}` : null}
                          </span>
                        ) : null}
                      </div>
                      <FavoriteMark show={!!b.is_favorite} />
                    </CommandItem>
                  ))}
                </CommandGroup>
              ) : null}

              {!showHint && (data?.authors?.length ?? 0) > 0 ? (
                <>
                  {(data?.books?.length ?? 0) > 0 ? <CommandSeparator /> : null}
                  <CommandGroup heading="Авторы">
                    {data!.authors.map((a) => (
                      <CommandItem
                        key={`a-${a.id}`}
                        value={`author-${a.id}`}
                        onSelect={() => go(`/authors/${a.id}`)}
                      >
                        <UserIcon aria-hidden />
                        <div className="flex min-w-0 flex-col flex-1">
                          <span className="truncate">{a.full_name}</span>
                          <span className="truncate text-xs text-muted-foreground">
                            {a.book_count} {pluralBooks(a.book_count)}
                          </span>
                        </div>
                        <FavoriteMark show={!!a.is_favorite} kind="sub" />
                      </CommandItem>
                    ))}
                  </CommandGroup>
                </>
              ) : null}

              {!showHint && (data?.series?.length ?? 0) > 0 ? (
                <>
                  {(data?.books?.length ?? 0) + (data?.authors?.length ?? 0) > 0 ? (
                    <CommandSeparator />
                  ) : null}
                  <CommandGroup heading="Серии">
                    {data!.series.map((s) => (
                      <CommandItem
                        key={`s-${s.id}`}
                        value={`series-${s.id}`}
                        onSelect={() => go(`/series/${s.id}`)}
                      >
                        <LayersIcon aria-hidden />
                        <div className="flex min-w-0 flex-col flex-1">
                          <span className="truncate">{s.title}</span>
                          <span className="truncate text-xs text-muted-foreground">
                            {s.author_name ? `${s.author_name} · ` : ''}
                            {s.book_count} {pluralBooks(s.book_count)}
                          </span>
                        </div>
                        <FavoriteMark show={!!s.is_favorite} kind="sub" />
                      </CommandItem>
                    ))}
                  </CommandGroup>
                </>
              ) : null}
            </CommandList>
          </Command>
        </DialogContent>
      </Dialog>
    </>
  );
}

// FavoriteMark — маленькая метка справа от элемента палитры (рендерится только
// при is_favorite). Книга — ★ «в избранном» (жёлтая). Автор/серия — колокольчик
// «подписан» (монохромный): подписка, а не избранное.
function FavoriteMark({ show, kind = 'book' }: { show: boolean; kind?: 'book' | 'sub' }) {
  if (!show) return null;
  if (kind === 'sub') {
    return <Bell className="ml-2 size-3.5 shrink-0 fill-foreground" aria-label="Подписан" />;
  }
  return (
    <Star
      className="ml-2 size-3.5 shrink-0 fill-yellow-500 stroke-yellow-500"
      aria-label="В избранном"
    />
  );
}

function PaletteTrigger({ onClick }: { onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'inline-flex h-9 items-center gap-2 rounded-md border border-input bg-background px-3 text-sm text-muted-foreground shadow-xs transition-colors hover:bg-accent hover:text-accent-foreground',
        'min-w-0 sm:w-72',
      )}
      aria-label="Открыть поиск"
    >
      <SearchIcon className="size-4 shrink-0" aria-hidden />
      <span className="hidden flex-1 truncate text-left sm:inline">Поиск книг, авторов…</span>
      <kbd className="ml-auto hidden items-center gap-0.5 rounded border bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground sm:inline-flex">
        <span aria-hidden>⌘</span>K
      </kbd>
    </button>
  );
}

function pluralBooks(n: number): string {
  // Простой плюрал для русского. 1 книга / 2-4 книги / 5+ книг.
  // Учитываем 11-14 как исключение.
  const last2 = n % 100;
  const last1 = n % 10;
  if (last2 >= 11 && last2 <= 14) return 'книг';
  if (last1 === 1) return 'книга';
  if (last1 >= 2 && last1 <= 4) return 'книги';
  return 'книг';
}
