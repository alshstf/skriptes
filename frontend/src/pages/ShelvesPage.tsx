import { useState } from 'react';
import { Link } from '@tanstack/react-router';
import { ChevronRight, FolderPlus, GripVertical, Library, Pencil, Star, Trash2 } from 'lucide-react';
import {
  DndContext,
  DragOverlay,
  KeyboardSensor,
  PointerSensor,
  TouchSensor,
  useDraggable,
  useDroppable,
  useSensor,
  useSensors,
  type DragEndEvent,
  type DragStartEvent,
} from '@dnd-kit/core';
import { Button } from '@/components/ui/button';
import { BookMeta } from '@/components/BookMeta';
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
  useMoveBookBetweenShelves,
  type Collection,
  type CollectionBook,
} from '@/lib/collections';
import { cn } from '@/lib/utils';

// dragData/dropData — типизированные payload'ы DnD. Draggable книги несёт исходную
// полку (sourceId) + название (для DragOverlay), droppable полка — id+имя (для тоста).
type DragData = { bookId: number; sourceId: number; title: string };
type DropData = { collId: number; name: string };

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
  const move = useMoveBookBetweenShelves();
  // Активная перетаскиваемая книга — для DragOverlay (плавающий чип под курсором/пальцем).
  const [drag, setDrag] = useState<DragData | null>(null);

  // Сенсоры: мышь (drag после сдвига 8px — клик/тап по книге и «убрать» не стартуют
  // drag), тач (long-press 220мс — чтобы свайп-скролл списка не превращался в drag),
  // клавиатура (space взять/бросить, стрелки — для доступности).
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 8 } }),
    useSensor(TouchSensor, { activationConstraint: { delay: 220, tolerance: 6 } }),
    useSensor(KeyboardSensor),
  );

  function onDragStart(e: DragStartEvent) {
    setDrag((e.active.data.current as DragData | undefined) ?? null);
  }
  function onDragEnd(e: DragEndEvent) {
    setDrag(null);
    const a = e.active.data.current as DragData | undefined;
    const o = e.over?.data.current as DropData | undefined;
    if (!a || !o || o.collId === a.sourceId) return; // бросок мимо/в ту же полку — no-op
    move.mutate({ bookId: a.bookId, fromId: a.sourceId, toId: o.collId, toName: o.name });
  }

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
        <DndContext
          sensors={sensors}
          onDragStart={onDragStart}
          onDragEnd={onDragEnd}
          onDragCancel={() => setDrag(null)}
        >
          <ul className="space-y-2">
            {collections.map((c) => (
              <ShelfRow key={c.id} collection={c} />
            ))}
          </ul>
          <DragOverlay>
            {drag ? (
              <div className="max-w-xs truncate rounded-md border border-border bg-popover px-3 py-1.5 text-sm font-medium shadow-md">
                {drag.title}
              </div>
            ) : null}
          </DragOverlay>
        </DndContext>
      )}
    </div>
  );
}

/**
 * ShelfRow — одна полка: заголовок (раскрытие книг) + действия
 * (переименовать/удалить). Книги полки грузятся лениво при раскрытии. Сама полка —
 * drop-зона: подсвечивается, когда над ней тащат книгу с ДРУГОЙ полки.
 */
function ShelfRow({ collection }: { collection: Collection }) {
  const [open, setOpen] = useState(false);
  const del = useDeleteCollection();
  // Служебная «Избранное» (★ книги): закреплена сверху, переименовать/удалить нельзя.
  const isFav = collection.kind === 'favorites';

  const { setNodeRef, isOver, active } = useDroppable({
    id: `shelf-${collection.id}`,
    data: { collId: collection.id, name: collection.name } satisfies DropData,
  });
  const activeData = active?.data.current as DragData | undefined;
  const isTarget = isOver && activeData != null && activeData.sourceId !== collection.id;

  return (
    <li
      ref={setNodeRef}
      className={cn(
        'rounded-md border transition',
        isTarget ? 'border-primary bg-accent/40 ring-2 ring-primary/40' : 'border-border',
      )}
    >
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
          {isFav ? (
            <Star className="size-3.5 shrink-0 fill-yellow-500 stroke-yellow-500" aria-hidden />
          ) : null}
          <span className="min-w-0 flex-1 truncate text-sm font-medium">{collection.name}</span>
          <span className="shrink-0 text-xs tabular-nums text-muted-foreground">
            {collection.book_count} кн.
          </span>
        </button>
        {/* Служебную полку не переименовать/не удалить (★ управляет ей с карточек). */}
        {!isFav ? (
          <>
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
          </>
        ) : null}
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
        Полка пуста — добавьте книги с их карточек или перетащите с другой полки.
      </p>
    );
  }
  return (
    <ul className="border-t border-border/60 px-2 py-1">
      {books.map((b) => (
        <ShelfBookRow
          key={b.id}
          book={b}
          collectionId={collectionId}
          onRemove={() => remove.mutate({ collectionId, bookId: b.id })}
          removing={remove.isPending}
        />
      ))}
    </ul>
  );
}

/**
 * ShelfBookRow — строка книги на полке + draggable (перенос на другую полку).
 * Listeners на всей строке (тащить можно за любую её часть; long-press на тач).
 * Клик по книге (без сдвига) ведёт на карточку; «убрать» — stopPropagation на
 * pointerdown, чтобы взаимодействие с кнопкой не стартовало drag.
 */
function ShelfBookRow({
  book,
  collectionId,
  onRemove,
  removing,
}: {
  book: CollectionBook;
  collectionId: number;
  onRemove: () => void;
  removing: boolean;
}) {
  const { setNodeRef, listeners, attributes, isDragging } = useDraggable({
    id: `book-${collectionId}-${book.id}`,
    data: { bookId: book.id, sourceId: collectionId, title: book.title } satisfies DragData,
  });
  return (
    <li
      ref={setNodeRef}
      {...attributes}
      {...listeners}
      className={cn(
        'flex items-center gap-1.5 rounded px-2 py-1.5 hover:bg-accent/30',
        isDragging ? 'opacity-40' : 'cursor-grab',
      )}
    >
      <GripVertical className="size-4 shrink-0 text-muted-foreground/50" aria-hidden />
      <Link
        to="/works/$id"
        params={{ id: String(book.work_id ?? book.id) }}
        className="min-w-0 flex-1"
        onClick={(e) => {
          if (isDragging) e.preventDefault(); // не навигируем после drag
        }}
      >
        <span className="block truncate text-sm font-medium">{book.title}</span>
        {book.authors.length > 0 ? (
          <span className="block truncate text-xs text-muted-foreground">
            {book.authors.join(', ')}
            {book.series ? ` · ${book.series}` : ''}
          </span>
        ) : null}
        <BookMeta book={book} />
      </Link>
      <Button
        variant="ghost"
        size="icon-sm"
        onPointerDown={(e) => e.stopPropagation()}
        onClick={onRemove}
        disabled={removing}
        aria-label={`Убрать «${book.title}» из полки`}
      >
        <Trash2 className="size-4" aria-hidden />
      </Button>
    </li>
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
        {/* Radix требует Description (или aria-describedby) у DialogContent —
            без него console-warning и пустая связка для скринридера. */}
        <DialogDescription className="sr-only">Введите новое название полки.</DialogDescription>
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
