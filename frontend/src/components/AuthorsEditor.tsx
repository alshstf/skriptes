import { useState } from 'react';
import { Link } from '@tanstack/react-router';
import { Pencil, Plus, RotateCcw, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Command, CommandInput, CommandItem, CommandList } from '@/components/ui/command';
import { useMe } from '@/lib/auth';
import { useSuggest } from '@/lib/suggest';
import { useSetOverride, useRevertOverride } from '@/lib/admin';
import { useLongPress } from '@/lib/useLongPress';
import { type AuthorRef } from '@/lib/books';

type Picked = { id: number; full_name: string };

/**
 * AuthorsEditor — авторы работы с правкой для админа (оверрайд authors, грабля №19).
 * Не-админ: ссылки на авторов (как было). Админ: ссылки + правка — десктоп ховер-
 * карандаш / мобила лонг-тап → поповер: упорядоченный список выбранных (✕ убрать) +
 * поиск по существующим авторам (useSuggest) для добавления. Первый — основной
 * (works.primary_author_id). Сохранение материализует набор на все издания работы.
 * Создание новых авторов — отдельный follow-up (пока только существующие).
 */
export function AuthorsEditor({
  workId,
  authors,
  overridden = false,
}: {
  workId: number;
  authors: AuthorRef[];
  overridden?: boolean;
}) {
  const me = useMe();
  const links =
    authors.length > 0 ? (
      <p className="text-base text-muted-foreground">
        {authors.map((a, i) => (
          <span key={a.id}>
            {i > 0 ? ', ' : ''}
            <Link to="/authors/$id" params={{ id: String(a.id) }} className="hover:underline">
              {a.full_name}
            </Link>
          </span>
        ))}
      </p>
    ) : null;
  if (me.data?.role !== 'admin') return links;
  return <AdminAuthors workId={workId} authors={authors} overridden={overridden} links={links} />;
}

function AdminAuthors({
  workId,
  authors,
  overridden,
  links,
}: {
  workId: number;
  authors: AuthorRef[];
  overridden: boolean;
  links: React.ReactNode;
}) {
  const setOverride = useSetOverride();
  const revert = useRevertOverride();
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const [draft, setDraft] = useState<Picked[]>([]);
  const longPress = useLongPress(() => startEdit());
  const suggest = useSuggest(open ? search : '', 8);

  function startEdit() {
    setDraft(authors.map((a) => ({ id: a.id, full_name: a.full_name })));
    setSearch('');
    setOpen(true);
  }
  const draftIds = new Set(draft.map((d) => d.id));
  const candidates = (suggest.data?.authors ?? []).filter((a) => !draftIds.has(a.id));
  function add(a: Picked) {
    setDraft((p) => (p.some((x) => x.id === a.id) ? p : [...p, { id: a.id, full_name: a.full_name }]));
    setSearch('');
  }
  function remove(id: number) {
    setDraft((p) => p.filter((x) => x.id !== id));
  }
  function save() {
    setOverride.mutate(
      { target_kind: 'work', target_id: workId, field: 'authors', value: { author_ids: draft.map((d) => d.id) } },
      { onSuccess: () => setOpen(false) },
    );
  }

  return (
    <div className="group/auth flex items-start gap-1" {...longPress}>
      {links ?? <span className="text-base italic text-muted-foreground">Авторы не указаны</span>}
      <Popover open={open} onOpenChange={(o) => (o ? startEdit() : setOpen(false))}>
        <PopoverTrigger asChild>
          <button
            type="button"
            aria-label="Изменить авторов"
            className="mt-1 shrink-0 rounded p-0.5 text-muted-foreground opacity-0 transition-opacity hover:text-foreground focus:opacity-100 group-hover/auth:opacity-100"
          >
            <Pencil className="size-3.5" aria-hidden />
          </button>
        </PopoverTrigger>
        <PopoverContent align="start" className="w-80 p-0">
          <div className="flex flex-wrap gap-1.5 border-b border-border p-2">
            {draft.length > 0 ? (
              draft.map((a) => (
                <span key={a.id} className="inline-flex items-center gap-1 rounded bg-muted px-2 py-0.5 text-sm">
                  {a.full_name}
                  <button
                    type="button"
                    onClick={() => remove(a.id)}
                    aria-label={`Убрать ${a.full_name}`}
                    className="text-muted-foreground hover:text-foreground"
                  >
                    <X className="size-3.5" aria-hidden />
                  </button>
                </span>
              ))
            ) : (
              <span className="px-1 text-sm italic text-muted-foreground">Авторы не выбраны</span>
            )}
          </div>
          <Command shouldFilter={false}>
            <CommandInput value={search} onValueChange={setSearch} placeholder="Добавить автора…" />
            <CommandList>
              {search.trim().length < 2 ? (
                <div className="py-4 text-center text-xs text-muted-foreground">Введите ≥2 символов</div>
              ) : candidates.length > 0 ? (
                candidates.map((a) => (
                  <CommandItem key={a.id} value={String(a.id)} onSelect={() => add(a)}>
                    <Plus className="mr-2 size-4 shrink-0 text-muted-foreground" aria-hidden />
                    <span className="flex-1 truncate">{a.full_name}</span>
                    <span className="ml-2 shrink-0 text-xs text-muted-foreground">{a.book_count} кн.</span>
                  </CommandItem>
                ))
              ) : (
                <div className="py-4 text-center text-xs text-muted-foreground">
                  {suggest.isFetching ? 'Поиск…' : 'Не найдено'}
                </div>
              )}
            </CommandList>
          </Command>
          <div className="flex items-center justify-between gap-2 border-t border-border p-2">
            <span className="text-xs text-muted-foreground">первый в списке — основной автор</span>
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
          onClick={() => revert.mutate({ target_kind: 'work', target_id: workId, field: 'authors' })}
          disabled={revert.isPending}
          aria-label="Отменить правку авторов"
          className="mt-1 shrink-0 rounded p-0.5 text-muted-foreground opacity-60 transition-opacity hover:text-foreground focus:opacity-100 group-hover/auth:opacity-100 disabled:opacity-30"
        >
          <RotateCcw className="size-3" aria-hidden />
        </button>
      ) : null}
    </div>
  );
}
