import { useState } from 'react';
import { Link } from '@tanstack/react-router';
import { ChevronRight, FolderPlus, Library, Pencil, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Callout } from '@/components/ui/callout';
import { Input } from '@/components/ui/input';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
import {
  useCollections,
  useCollectionBooks,
  useCreateCollection,
  useRenameCollection,
  useDeleteCollection,
  useRemoveBookFromCollection,
  type Collection,
} from '@/lib/collections';
import { cn } from '@/lib/utils';

/**
 * ShelvesPage — /shelves: личные полки (коллекции) пользователя. Создать/
 * переименовать/удалить, раскрыть полку и посмотреть её книги. Книги кладутся
 * на полку с карточки книги («Добавить на полку»). Доступ — из меню юзера.
 *
 * Вынесено из раздела «Жанры» (полки — личная библиотека, а не каталог-браузинг);
 * на /genres остался только обзор жанров.
 */
export function ShelvesPage() {
  const collectionsQ = useCollections();
  const collections = collectionsQ.data ?? [];

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-2">
        <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight">
          <Library className="size-6" aria-hidden />
          Мои полки
        </h1>
        <CreateShelfDialog />
      </div>

      {collectionsQ.isLoading ? (
        <p className="text-sm italic text-muted-foreground">Загрузка…</p>
      ) : collections.length === 0 ? (
        <Callout icon={<Library className="size-4 shrink-0" aria-hidden />}>
          Полок пока нет. Создайте полку и складывайте в неё книги вручную — с карточки любой
          книги через «Добавить на полку».
        </Callout>
      ) : (
        <ul className="space-y-2">
          {collections.map((c) => (
            <ShelfRow key={c.id} collection={c} />
          ))}
        </ul>
      )}
    </div>
  );
}

/**
 * ShelfRow — одна полка: заголовок (раскрытие книг) + действия
 * (переименовать/удалить). Книги полки грузятся лениво при раскрытии.
 */
function ShelfRow({ collection }: { collection: Collection }) {
  const [open, setOpen] = useState(false);
  const del = useDeleteCollection();

  return (
    <li className="rounded-md border border-border">
      <div className="flex items-center gap-1.5 p-2">
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          aria-label={open ? 'Свернуть полку' : 'Раскрыть полку'}
          className="flex min-w-0 flex-1 items-center gap-2 rounded px-1 py-1 text-left transition hover:bg-accent/30"
        >
          <ChevronRight
            className={cn('size-4 shrink-0 transition-transform', open ? 'rotate-90' : '')}
            aria-hidden
          />
          <span className="min-w-0 flex-1 truncate text-sm font-medium">{collection.name}</span>
          <span className="shrink-0 text-xs tabular-nums text-muted-foreground">
            {collection.book_count} кн.
          </span>
        </button>
        <RenameShelfDialog collection={collection} />
        <Button
          variant="ghost"
          size="icon-sm"
          onClick={() => {
            if (
              window.confirm(`Удалить полку «${collection.name}»? Книги останутся в библиотеке.`)
            ) {
              del.mutate(collection.id);
            }
          }}
          disabled={del.isPending}
          aria-label={`Удалить полку «${collection.name}»`}
        >
          <Trash2 className="size-4" aria-hidden />
        </Button>
      </div>
      {open ? <ShelfBooks collectionId={collection.id} /> : null}
    </li>
  );
}

function ShelfBooks({ collectionId }: { collectionId: number }) {
  const booksQ = useCollectionBooks(collectionId);
  const remove = useRemoveBookFromCollection();
  const books = booksQ.data ?? [];

  if (booksQ.isLoading) {
    return <p className="px-4 pb-3 text-sm italic text-muted-foreground">Загрузка…</p>;
  }
  if (books.length === 0) {
    return (
      <p className="px-4 pb-3 text-sm italic text-muted-foreground">
        Полка пуста — добавьте книги с их карточек.
      </p>
    );
  }
  return (
    <ul className="border-t border-border/60 px-2 py-1">
      {books.map((b) => (
        <li key={b.id} className="flex items-center gap-2 rounded px-2 py-1.5 hover:bg-accent/30">
          <Link
            to="/works/$id"
            params={{ id: String(b.work_id ?? b.id) }}
            className="min-w-0 flex-1"
          >
            <span className="block truncate text-sm font-medium">{b.title}</span>
            {b.authors.length > 0 ? (
              <span className="block truncate text-xs text-muted-foreground">
                {b.authors.join(', ')}
                {b.series ? ` · ${b.series}` : ''}
              </span>
            ) : null}
          </Link>
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={() => remove.mutate({ collectionId, bookId: b.id })}
            disabled={remove.isPending}
            aria-label={`Убрать «${b.title}» из полки`}
          >
            <Trash2 className="size-4" aria-hidden />
          </Button>
        </li>
      ))}
    </ul>
  );
}

// ── Диалоги полок ───────────────────────────────────────────────────

function CreateShelfDialog() {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const create = useCreateCollection();
  const trimmed = name.trim();

  const submit = () => {
    if (!trimmed) return;
    create.mutate(trimmed, {
      onSuccess: () => {
        setOpen(false);
        setName('');
      },
    });
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (!o) setName('');
      }}
    >
      <DialogTrigger asChild>
        <Button size="sm" className="gap-1">
          <FolderPlus className="size-4" aria-hidden />
          Новая полка
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-sm">
        <div className="space-y-1">
          <DialogTitle>Новая полка</DialogTitle>
          <DialogDescription>Личный список книг, который вы собираете вручную.</DialogDescription>
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
            {create.isPending ? 'Создание…' : 'Создать'}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function RenameShelfDialog({ collection }: { collection: Collection }) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState(collection.name);
  const rename = useRenameCollection();
  const trimmed = name.trim();

  const submit = () => {
    if (!trimmed || trimmed === collection.name) {
      setOpen(false);
      return;
    }
    rename.mutate({ id: collection.id, name: trimmed }, { onSuccess: () => setOpen(false) });
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (o) setName(collection.name);
      }}
    >
      <DialogTrigger asChild>
        <Button variant="ghost" size="icon-sm" aria-label={`Переименовать полку «${collection.name}»`}>
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
          placeholder="Название полки"
          aria-label="Новое название полки"
        />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={() => setOpen(false)}>
            Отмена
          </Button>
          <Button size="sm" onClick={submit} disabled={!trimmed || rename.isPending}>
            {rename.isPending ? 'Сохранение…' : 'Сохранить'}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
