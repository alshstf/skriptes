import { useState } from 'react';
import { Check, FolderPlus, Library, Pencil, Plus } from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Callout } from '@/components/ui/callout';
import {
  useCollections,
  useCollectionBooks,
  useCreateCollection,
  useAddBookToCollection,
  useRemoveBookFromCollection,
  type Collection,
} from '@/lib/collections';
import { cn } from '@/lib/utils';

/**
 * AddToShelfDialog — действие «Добавить на полку» на карточке книги.
 *
 * Диалог: список полок пользователя (toggle членства книги в каждой) + поле
 * «создать новую полку». Всегда доступен залогиненному юзеру; если полок нет —
 * внутри предлагается создать первую (компонент не скрывается).
 *
 * bookId — id ИЗДАНИЯ (book.id), членство в полке привязано к конкретному
 * fb2-файлу (как favorites/reads — по book_id, а не work_id).
 */
/**
 * compact — триггер живёт в блоке «полки книги» под мета (см. BookDetailPage):
 *  - false (книга ни на одной полке): основная кнопка «На полку»;
 *  - true (рядом со списком полок): компактное «Изменить».
 */
export function AddToShelfDialog({ bookId, compact = false }: { bookId: number; compact?: boolean }) {
  const [open, setOpen] = useState(false);

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        {compact ? (
          <Button
            variant="ghost"
            size="sm"
            className="h-auto gap-1 px-1.5 py-0.5 text-xs"
            aria-label="Изменить полки книги"
          >
            <Pencil className="size-3" aria-hidden />
            Изменить
          </Button>
        ) : (
          <Button variant="ghost" size="sm" className="gap-1" aria-label="Добавить на полку">
            <Library className="size-4" aria-hidden />
            На полку
          </Button>
        )}
      </DialogTrigger>
      <DialogContent className="max-w-sm">
        <div className="space-y-1">
          <DialogTitle>Добавить на полку</DialogTitle>
          <DialogDescription>Личные списки книг, которые вы собираете вручную.</DialogDescription>
        </div>
        {open ? <ShelvesBody bookId={bookId} /> : null}
      </DialogContent>
    </Dialog>
  );
}

function ShelvesBody({ bookId }: { bookId: number }) {
  const collectionsQ = useCollections();
  const collections = collectionsQ.data ?? [];

  return (
    <div className="space-y-3">
      {collectionsQ.isLoading ? (
        <p className="text-sm italic text-muted-foreground">Загрузка…</p>
      ) : collections.length === 0 ? (
        <Callout icon={<Library className="size-4 shrink-0" aria-hidden />}>
          Полок пока нет — создайте первую ниже.
        </Callout>
      ) : (
        <ul className="max-h-[40vh] space-y-1 overflow-y-auto">
          {collections.map((c) => (
            <ShelfToggleRow key={c.id} collection={c} bookId={bookId} />
          ))}
        </ul>
      )}
      <CreateInline />
    </div>
  );
}

/**
 * ShelfToggleRow — строка-чекбокс: книга в полке или нет. Состояние membership
 * читаем из списка книг полки (useCollectionBooks). Клик добавляет/убирает.
 */
function ShelfToggleRow({ collection, bookId }: { collection: Collection; bookId: number }) {
  const booksQ = useCollectionBooks(collection.id);
  const add = useAddBookToCollection();
  const remove = useRemoveBookFromCollection();
  const inShelf = (booksQ.data ?? []).some((b) => b.id === bookId);
  const busy = add.isPending || remove.isPending || booksQ.isLoading;

  const toggle = () => {
    if (inShelf) {
      remove.mutate({ collectionId: collection.id, bookId });
    } else {
      add.mutate({ collectionId: collection.id, bookId });
    }
  };

  return (
    <li>
      <button
        type="button"
        onClick={toggle}
        disabled={busy}
        aria-pressed={inShelf}
        className="flex w-full items-center gap-2 rounded-md border border-border px-3 py-2 text-left text-sm transition hover:bg-accent/30 disabled:opacity-50"
      >
        <span
          className={cn(
            'flex size-4 shrink-0 items-center justify-center rounded border',
            inShelf ? 'border-primary bg-primary text-primary-foreground' : 'border-input',
          )}
          aria-hidden
        >
          {inShelf ? <Check className="size-3" /> : null}
        </span>
        <span className="min-w-0 flex-1 truncate">{collection.name}</span>
        <span className="shrink-0 text-xs tabular-nums text-muted-foreground">
          {collection.book_count}
        </span>
      </button>
    </li>
  );
}

/**
 * CreateInline — поле создания новой полки прямо в диалоге. После создания
 * полка появляется в списке (инвалидация кэша); книгу пользователь добавит
 * кликом по новой строке (явный шаг — без неявного авто-добавления).
 */
function CreateInline() {
  const [name, setName] = useState('');
  const create = useCreateCollection();
  const trimmed = name.trim();

  const submit = () => {
    if (!trimmed) return;
    create.mutate(trimmed, { onSuccess: () => setName('') });
  };

  return (
    <div className="flex items-center gap-2 border-t border-border/60 pt-3">
      <FolderPlus className="size-4 shrink-0 text-muted-foreground" aria-hidden />
      <Input
        value={name}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') submit();
        }}
        placeholder="Новая полка…"
        aria-label="Название новой полки"
        className="h-8 text-sm"
      />
      <Button size="icon-sm" onClick={submit} disabled={!trimmed || create.isPending} aria-label="Создать полку">
        <Plus className="size-4" aria-hidden />
      </Button>
    </div>
  );
}
