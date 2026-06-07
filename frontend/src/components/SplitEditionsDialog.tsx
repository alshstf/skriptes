import { useState } from 'react';
import { Check, Scissors } from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
  DialogTrigger,
} from '@/components/ui/dialog';
import { useSplitEditions } from '@/lib/admin';
import { useMe } from '@/lib/auth';
import { useLanguageMap } from '@/lib/content';
import { cn } from '@/lib/utils';
import type { EditionRef } from '@/lib/books';

/**
 * SplitEditionsDialog — ручное разъединение (admin): выносит выбранные ИЗДАНИЯ
 * работы в новую отдельную книгу (починка ложного слияния — в т.ч. авто-Tier-1.5).
 * Выбор кликом по строке. Нельзя вынести 0 или ВСЕ издания (это no-op). Не-админам
 * и для одного издания не рендерится.
 */
export function SplitEditionsDialog({
  editions,
  workTitle,
}: {
  editions: EditionRef[];
  workTitle: string;
}) {
  const { data: me } = useMe();
  const langMap = useLanguageMap();
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const split = useSplitEditions();

  if (me?.role !== 'admin' || editions.length < 2) return null;

  const reset = () => setSelected(new Set());
  const toggle = (id: number) =>
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });
  // Нельзя вынести ничего или ВСЕ издания (новая работа = старая, бессмысленно).
  const valid = selected.size >= 1 && selected.size < editions.length;
  const onSplit = () => {
    if (!valid) return;
    split.mutate(
      { book_ids: [...selected] },
      {
        onSuccess: () => {
          reset();
          setOpen(false);
        },
      },
    );
  };

  // Заголовок строки = СОБСТВЕННОЕ название издания (так «чужое» издание сразу
  // видно); мета — язык/серия/переводчик/год/размер для осознанного выбора.
  const editionTitle = (e: EditionRef) => e.title || e.edition_title || workTitle;
  const editionMeta = (e: EditionRef) => {
    const parts: string[] = [];
    if (e.lang) parts.push((langMap.get(e.lang) ?? e.lang).toUpperCase());
    if (e.series?.title) parts.push(`Серия: ${e.series.title}`);
    if (e.translator) parts.push(e.translator);
    if (e.edition_year) parts.push(String(e.edition_year));
    parts.push(`${(e.size_bytes / 1024).toFixed(0)} KiB`);
    return parts.join(' · ');
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
          <Scissors className="size-4" aria-hidden />
          Разделить
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-lg">
        <div className="space-y-1">
          <DialogTitle>Разделить издания</DialogTitle>
          <DialogDescription>
            Отметьте издания, которые на самом деле ДРУГАЯ книга. Отмеченные будут
            вынесены в новую отдельную книгу, остальные останутся в «{workTitle}».
          </DialogDescription>
        </div>
        <ul className="max-h-[50vh] space-y-1 overflow-y-auto py-1">
          {editions.map((e) => {
            const on = selected.has(e.id);
            return (
              <li key={e.id}>
                <button
                  type="button"
                  onClick={() => toggle(e.id)}
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
                  <span className="flex min-w-0 flex-1 flex-col">
                    <span className="truncate">{editionTitle(e)}</span>
                    <span className="truncate text-xs text-muted-foreground">{editionMeta(e)}</span>
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
        {selected.size === editions.length ? (
          <p className="text-xs text-muted-foreground">
            Нельзя вынести все издания — оставьте хотя бы одно в текущей книге.
          </p>
        ) : null}
        <div className="flex justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={() => setOpen(false)}>
            Отмена
          </Button>
          <Button
            variant="secondary"
            size="sm"
            disabled={!valid || split.isPending}
            onClick={onSplit}
          >
            Вынести{selected.size >= 1 ? ` (${selected.size})` : ''}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
