import { useEffect, useState } from 'react';
import { Bookmark, Highlighter, Trash2, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { placeLabel, type Annotation } from '@/lib/annotations';

// Закладки и выделения с заметками в ридере (#389): список, полоса действий
// над выделением, диалог заметки.

/** AnnotationsSheet — «Заметки»: закладки и выделения по ходу книги. */
export function AnnotationsSheet({
  open,
  onOpenChange,
  items,
  onGo,
  onDelete,
  onEdit,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  items: Annotation[];
  onGo: (a: Annotation) => void;
  onDelete: (a: Annotation) => void;
  onEdit: (a: Annotation) => void;
}) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-[90%] gap-0 p-0 sm:max-w-md">
        <SheetHeader className="border-b border-border">
          <SheetTitle>Заметки</SheetTitle>
          <SheetDescription>Закладки и выделения в этой книге.</SheetDescription>
        </SheetHeader>
        {items.length === 0 ? (
          <p className="p-4 text-sm text-pretty text-muted-foreground">
            Пока пусто. Выделите текст, чтобы подсветить его или оставить заметку, а закладку поставьте кнопкой в
            верхней панели.
          </p>
        ) : (
          <ul className="flex-1 divide-y divide-border overflow-y-auto">
            {items.map((a) => (
              <li key={a.id} className="flex gap-2 px-4 py-3">
                <button type="button" className="min-w-0 flex-1 space-y-1 text-left" onClick={() => onGo(a)}>
                  <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
                    {a.kind === 'bookmark' ? (
                      <Bookmark className="size-3.5 shrink-0" aria-hidden />
                    ) : (
                      <Highlighter className="size-3.5 shrink-0" aria-hidden />
                    )}
                    {a.kind === 'bookmark' ? 'Закладка' : 'Выделение'}
                    {placeLabel(a) ? ` · ${placeLabel(a)}` : ''}
                  </span>
                  {a.excerpt ? <span className="line-clamp-3 block text-sm text-pretty">«{a.excerpt}»</span> : null}
                  {a.note ? <span className="block text-sm text-pretty text-muted-foreground">{a.note}</span> : null}
                </button>
                <div className="flex shrink-0 flex-col gap-1">
                  {a.kind === 'highlight' ? (
                    <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={() => onEdit(a)}>
                      Заметка
                    </Button>
                  ) : null}
                  <Button variant="ghost" size="icon-sm" aria-label="Удалить" onClick={() => onDelete(a)}>
                    <Trash2 className="size-4" aria-hidden />
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </SheetContent>
    </Sheet>
  );
}

/** SelectionBar — действия над выделенным текстом: подсветить или с заметкой. */
export function SelectionBar({
  text,
  busy,
  onHighlight,
  onNote,
  onCancel,
}: {
  text: string;
  busy: boolean;
  onHighlight: () => void;
  onNote: () => void;
  onCancel: () => void;
}) {
  return (
    <div
      role="toolbar"
      aria-label="Выделенный текст"
      className="absolute inset-x-3 bottom-[calc(0.75rem+env(safe-area-inset-bottom))] z-10 mx-auto flex max-w-xl items-center gap-2 rounded-lg border border-border bg-popover p-2 shadow-lg"
    >
      <span className="min-w-0 flex-1 truncate px-1 text-sm text-muted-foreground">«{text}»</span>
      <Button size="sm" variant="outline" className="gap-1" disabled={busy} onClick={onHighlight}>
        <Highlighter className="size-4" aria-hidden />
        Выделить
      </Button>
      <Button size="sm" disabled={busy} onClick={onNote}>
        Заметка
      </Button>
      <Button size="icon-sm" variant="ghost" aria-label="Отменить" onClick={onCancel}>
        <X className="size-4" aria-hidden />
      </Button>
    </div>
  );
}

/** NoteDialog — заметка к выделению: новая или правка; удаление выделения. */
export function NoteDialog({
  open,
  excerpt,
  initial,
  busy,
  onSave,
  onDelete,
  onClose,
}: {
  open: boolean;
  excerpt: string;
  initial: string;
  busy: boolean;
  onSave: (note: string) => void;
  onDelete?: () => void;
  onClose: () => void;
}) {
  const [note, setNote] = useState(initial);
  useEffect(() => {
    if (open) setNote(initial);
  }, [open, initial]);
  return (
    <Dialog open={open} onOpenChange={(o) => (o ? null : onClose())}>
      <DialogContent className="max-w-md">
        <DialogTitle>Заметка</DialogTitle>
        <DialogDescription className="line-clamp-4 text-pretty">«{excerpt}»</DialogDescription>
        <textarea
          autoFocus
          value={note}
          onChange={(e) => setNote(e.target.value)}
          rows={5}
          maxLength={10000}
          aria-label="Текст заметки"
          className="w-full resize-y rounded-md border border-input bg-background px-3 py-2 text-sm shadow-xs focus-visible:ring-2 focus-visible:ring-ring"
        />
        <div className="flex items-center gap-2">
          {onDelete ? (
            <Button variant="ghost" size="sm" className="gap-1 text-destructive" disabled={busy} onClick={onDelete}>
              <Trash2 className="size-4" aria-hidden />
              Убрать выделение
            </Button>
          ) : null}
          <div className="ml-auto flex gap-2">
            <Button variant="ghost" size="sm" onClick={onClose}>
              Отмена
            </Button>
            <Button size="sm" disabled={busy} onClick={() => onSave(note)}>
              Сохранить
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
