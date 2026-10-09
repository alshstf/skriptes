import { useState } from 'react';
import { Link } from '@tanstack/react-router';
import { BookmarkPlus, ChevronRight, Pencil, SlidersHorizontal, Trash2 } from 'lucide-react';
import { BookListItem } from '@/components/BookListItem';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Dialog, DialogContent, DialogDescription, DialogTitle, DialogTrigger } from '@/components/ui/dialog';
import {
  useCreateSmartShelf,
  useDeleteSmartShelf,
  useDescribeFilters,
  useRenameSmartShelf,
  useSmartShelfBooks,
  useSmartShelves,
  type SmartFilters,
  type SmartShelf,
} from '@/lib/smartShelves';
import { useOpenToggle } from '@/lib/openState';
import { cn } from '@/lib/utils';

// Умные полки (#389): кнопка «Сохранить как полку» на /books и блок полок на
// /shelves. Полка — сохранённые фильтры, книги считает поиск при открытии.

/** SaveSmartShelfButton — сохранить текущие фильтры /books как умную полку. */
export function SaveSmartShelfButton({ filters }: { filters: SmartFilters }) {
  const describe = useDescribeFilters();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const create = useCreateSmartShelf();
  const trimmed = name.trim();
  const submit = () => {
    if (!trimmed) return;
    create.mutate({ name: trimmed, filters }, { onSuccess: () => setOpen(false) });
  };
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (o) setName(capitalize(describe(filters)).slice(0, 80));
      }}
    >
      <DialogTrigger asChild>
        <Button variant="ghost" size="sm" className="h-7 gap-1 px-2 text-xs text-muted-foreground">
          <BookmarkPlus className="size-3.5" aria-hidden />
          Сохранить как полку
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-sm">
        <div className="space-y-1">
          <DialogTitle>Умная полка</DialogTitle>
          <DialogDescription className="text-pretty">
            Полка запомнит фильтры и сама будет пополняться новыми книгами: {describe(filters)}.
          </DialogDescription>
        </div>
        <Input
          autoFocus
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') submit();
          }}
          placeholder="Название полки"
          aria-label="Название полки"
        />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={() => setOpen(false)}>
            Отмена
          </Button>
          <Button size="sm" onClick={submit} disabled={!trimmed || create.isPending}>
            {create.isPending ? 'Сохранение…' : 'Сохранить'}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function capitalize(s: string): string {
  return s ? s[0].toUpperCase() + s.slice(1) : s;
}

/** SmartShelvesSection — умные полки на /shelves (нет полок — блока нет). */
export function SmartShelvesSection() {
  const q = useSmartShelves();
  const shelves = q.data ?? [];
  if (shelves.length === 0) return null;
  return (
    <section aria-labelledby="smart-shelves-title" className="space-y-2">
      <h2 id="smart-shelves-title" className="flex items-center gap-2 text-sm font-medium text-muted-foreground">
        <SlidersHorizontal className="size-4" aria-hidden />
        Умные полки
      </h2>
      <ul className="space-y-2">
        {shelves.map((s) => (
          <SmartShelfRow key={s.id} shelf={s} />
        ))}
      </ul>
    </section>
  );
}

// PREVIEW — сколько книг полки показываем раскрытой; остальное — в каталоге.
const PREVIEW = 20;

function SmartShelfRow({ shelf }: { shelf: SmartShelf }) {
  const describe = useDescribeFilters();
  const [open, toggle] = useOpenToggle(`s:${shelf.id}`);
  // Счётчик — запрос на одну книгу; раскрытая полка — на PREVIEW (общий total).
  const count = useSmartShelfBooks(shelf.filters, 1);
  const total = count.data?.total;
  return (
    <li className="rounded-md border border-border">
      <div className="flex items-center gap-1 pr-1">
        <button
          type="button"
          onClick={toggle}
          aria-expanded={open}
          className="flex min-w-0 flex-1 items-center gap-2 rounded-md px-3 py-3 text-left transition hover:bg-accent/30"
        >
          <ChevronRight className={cn('size-4 shrink-0 transition-transform', open ? 'rotate-90' : '')} aria-hidden />
          <span className="min-w-0 flex-1">
            <span className="block truncate text-sm font-medium">{shelf.name}</span>
            <span className="block text-xs text-pretty text-muted-foreground">{describe(shelf.filters)}</span>
          </span>
          {total != null ? (
            <span className="shrink-0 text-xs tabular-nums text-muted-foreground">
              {total.toLocaleString('ru-RU')} кн.
            </span>
          ) : null}
        </button>
        <RenameSmartShelfDialog shelf={shelf} />
        <DeleteSmartShelfDialog shelf={shelf} />
      </div>
      {open ? <SmartShelfBooks shelf={shelf} /> : null}
    </li>
  );
}

function SmartShelfBooks({ shelf }: { shelf: SmartShelf }) {
  const q = useSmartShelfBooks(shelf.filters, PREVIEW);
  if (q.isLoading) return <p className="px-4 pb-3 text-sm italic text-muted-foreground">Загрузка…</p>;
  const items = q.data?.items ?? [];
  const total = q.data?.total ?? 0;
  return (
    <div className="border-t border-border/60">
      {items.length === 0 ? (
        <p className="px-4 py-3 text-sm italic text-muted-foreground">Сейчас под фильтры ничего не подходит.</p>
      ) : (
        <ul className="divide-y divide-border/60">
          {items.map((b) => (
            <li key={b.id}>
              <BookListItem book={b} showCover />
            </li>
          ))}
        </ul>
      )}
      <div className="border-t border-border/60 px-3 py-2">
        <Link
          to="/books"
          search={shelf.filters}
          className="text-sm text-muted-foreground transition hover:text-foreground"
        >
          {total > items.length
            ? `Все ${total.toLocaleString('ru-RU')} — открыть в каталоге`
            : 'Открыть в каталоге'}
        </Link>
      </div>
    </div>
  );
}

function RenameSmartShelfDialog({ shelf }: { shelf: SmartShelf }) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState(shelf.name);
  const rename = useRenameSmartShelf();
  const trimmed = name.trim();
  const submit = () => {
    if (!trimmed || trimmed === shelf.name) {
      setOpen(false);
      return;
    }
    rename.mutate({ id: shelf.id, name: trimmed }, { onSuccess: () => setOpen(false) });
  };
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (o) setName(shelf.name);
      }}
    >
      <DialogTrigger asChild>
        <Button variant="ghost" size="icon" className="size-8 text-muted-foreground" aria-label={`Переименовать «${shelf.name}»`}>
          <Pencil className="size-4" aria-hidden />
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-sm">
        <DialogTitle>Переименовать полку</DialogTitle>
        <Input
          autoFocus
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') submit();
          }}
          aria-label="Название полки"
        />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={() => setOpen(false)}>
            Отмена
          </Button>
          <Button size="sm" onClick={submit} disabled={!trimmed || rename.isPending}>
            Сохранить
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function DeleteSmartShelfDialog({ shelf }: { shelf: SmartShelf }) {
  const [open, setOpen] = useState(false);
  const del = useDeleteSmartShelf();
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="ghost" size="icon" className="size-8 text-muted-foreground" aria-label={`Удалить «${shelf.name}»`}>
          <Trash2 className="size-4" aria-hidden />
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-sm">
        <div className="space-y-1">
          <DialogTitle>Удалить полку «{shelf.name}»?</DialogTitle>
          <DialogDescription>Книги останутся в каталоге — удаляются только сохранённые фильтры.</DialogDescription>
        </div>
        <div className="flex justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={() => setOpen(false)}>
            Отмена
          </Button>
          <Button
            variant="destructive"
            size="sm"
            disabled={del.isPending}
            onClick={() => del.mutate(shelf.id, { onSuccess: () => setOpen(false) })}
          >
            Удалить
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
