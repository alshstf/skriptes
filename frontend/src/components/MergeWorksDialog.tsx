import { useState } from 'react';
import { Check, GitMerge } from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
  DialogTrigger,
} from '@/components/ui/dialog';
import { useMergeWorks } from '@/lib/admin';
import { useMe } from '@/lib/auth';
import { cn } from '@/lib/utils';
import type { BookListItem } from '@/lib/books';

/**
 * MergeWorksDialog — ручное объединение работ (admin). В отличие от
 * MergeSuggestions (авто-подсказки по ser_no) позволяет выбрать ПРОИЗВОЛЬНЫЕ 2+
 * книги списка серии/автора, которые на самом деле одно произведение, и слить их
 * в одну книгу с несколькими изданиями. Чекбокса в ui нет → выбор кликом по
 * строке (aria-pressed + check-иконка). Не-админам не рендерится.
 */
export function MergeWorksDialog({ books }: { books: BookListItem[] }) {
  const { data: me } = useMe();
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const merge = useMergeWorks();

  if (me?.role !== 'admin' || books.length < 2) return null;

  const reset = () => setSelected(new Set());
  const toggle = (wid: number) =>
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(wid)) n.delete(wid);
      else n.add(wid);
      return n;
    });
  const onMerge = () => {
    if (selected.size < 2) return;
    merge.mutate(
      { work_ids: [...selected] },
      {
        onSuccess: () => {
          reset();
          setOpen(false);
        },
      },
    );
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (!o) reset();
      }}
    >
      <DialogTrigger asChild>
        <Button variant="ghost" size="sm" className="gap-1 text-muted-foreground">
          <GitMerge className="size-4" aria-hidden />
          Объединить издания
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-lg">
        <div className="space-y-1">
          <DialogTitle>Объединить издания</DialogTitle>
          <DialogDescription>
            Отметьте 2 или больше книги, которые на самом деле одно произведение, — они
            станут одной книгой с несколькими изданиями. Отменить можно «Разделить» на
            карточке книги.
          </DialogDescription>
        </div>
        <ul className="max-h-[50vh] space-y-1 overflow-y-auto py-1">
          {books.map((b) => {
            const wid = b.work_id ?? b.id;
            const on = selected.has(wid);
            return (
              <li key={b.id}>
                <button
                  type="button"
                  onClick={() => toggle(wid)}
                  aria-pressed={on}
                  className={cn(
                    'flex w-full items-center gap-2 rounded-md border px-3 py-2 text-left text-sm transition',
                    on ? 'border-foreground bg-accent/50' : 'border-border hover:bg-accent/30',
                  )}
                >
                  <span
                    className={cn(
                      'flex size-4 shrink-0 items-center justify-center rounded-[4px] border',
                      on ? 'bg-foreground text-background' : 'border-muted-foreground/60',
                    )}
                    aria-hidden
                  >
                    {on ? <Check className="size-3" /> : null}
                  </span>
                  <span className="min-w-0 flex-1 truncate">
                    {typeof b.ser_no === 'number' && b.ser_no > 0 ? (
                      <span className="text-muted-foreground tabular-nums">#{b.ser_no} · </span>
                    ) : null}
                    {b.title}
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
        <div className="flex justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={() => setOpen(false)}>
            Отмена
          </Button>
          <Button
            variant="secondary"
            size="sm"
            disabled={selected.size < 2 || merge.isPending}
            onClick={onMerge}
          >
            Объединить{selected.size >= 2 ? ` (${selected.size})` : ''}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
