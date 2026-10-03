import { useState } from 'react';
import { useNavigate } from '@tanstack/react-router';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogTitle, DialogTrigger } from '@/components/ui/dialog';
import { useMergeAuthors } from '@/lib/admin';
import { pluralBooks } from '@/lib/format';
import { cn } from '@/lib/utils';

export type MergeSide = { id: number; full_name: string; book_count: number };

/**
 * MergeAuthorsDialog — слияние двух записей одного автора (#308): админ выбирает,
 * какая остаётся (по умолчанию — с бо́льшим числом книг). Книги, подписки и
 * биография (если у оставшейся её нет) переходят к ней; слияние помнится для
 * следующих импортов, отдельную книгу можно вернуть на её карточке.
 */
export function MergeAuthorsDialog({
  a,
  b,
  trigger = 'Объединить…',
  navigateToKept = false,
}: {
  a: MergeSide;
  b: MergeSide;
  trigger?: string;
  /** Перейти на карточку оставленной записи (с карточки слитой — она опустеет). */
  navigateToKept?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [keep, setKeep] = useState(a.book_count >= b.book_count ? a.id : b.id);
  const merge = useMergeAuthors();
  const navigate = useNavigate();
  const kept = keep === a.id ? a : b;
  const gone = keep === a.id ? b : a;

  const onMerge = () => {
    merge.mutate(
      { sourceId: gone.id, targetId: kept.id },
      {
        onSuccess: () => {
          setOpen(false);
          if (navigateToKept) void navigate({ to: '/authors/$id', params: { id: String(kept.id) } });
        },
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm">
          {trigger}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <div className="space-y-1.5">
          <DialogTitle>Объединить авторов</DialogTitle>
          <DialogDescription className="text-pretty">
            Книги, подписки и биография (если у оставшейся записи её нет) перейдут к оставленной записи. Слияние
            запомнится и для следующих импортов; отдельную книгу можно вернуть на её карточке.
          </DialogDescription>
        </div>
        <fieldset className="space-y-2">
          <legend className="text-sm font-medium">Какую запись оставить</legend>
          {[a, b].map((side) => (
            <button
              key={side.id}
              type="button"
              aria-pressed={keep === side.id}
              onClick={() => setKeep(side.id)}
              className={cn(
                'flex w-full items-center justify-between gap-3 rounded-md border px-3 py-2 text-left text-sm transition-colors',
                keep === side.id ? 'border-foreground bg-muted' : 'border-border hover:bg-muted/50',
              )}
            >
              <span className="min-w-0 truncate font-medium">{side.full_name}</span>
              <span className="shrink-0 tabular-nums text-muted-foreground">
                {side.book_count} {pluralBooks(side.book_count)}
              </span>
            </button>
          ))}
        </fieldset>
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={() => setOpen(false)}>
            Отмена
          </Button>
          <Button onClick={onMerge} disabled={merge.isPending}>
            {merge.isPending ? 'Объединяем…' : `Оставить «${kept.full_name}»`}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
