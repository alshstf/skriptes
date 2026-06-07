import { useState } from 'react';
import { Check, Lock, Scissors } from 'lucide-react';
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
 * SplitEditionsDialog — ручное разъединение (admin): выносит издания в новую
 * книгу (починка ложного слияния, в т.ч. авто-Tier-1.5).
 *
 * ЯКОРНОЕ издание (его название = названию работы, `is_anchor`) выносить НЕЛЬЗЯ —
 * оно держит идентичность работы; показываем его залоченным. Поэтому выбор —
 * только среди НЕ-якорных:
 *  - ровно один не-якорь (часто при 2 изданиях) → выбора нет, только
 *    подтверждение «вынести его»;
 *  - несколько не-якорей → чек-лист.
 * Не-админам и для одного издания не рендерится.
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

  const anchor = editions.find((e) => e.is_anchor) ?? null;
  const nonAnchors = editions.filter((e) => !e.is_anchor);
  // При ≥2 изданиях якорь всегда есть → не-якорей ≥1. Подстраховка:
  if (nonAnchors.length === 0) return null;

  // Один не-якорь → выносим его без чек-листа (только подтверждение).
  const single = nonAnchors.length === 1 ? nonAnchors[0] : null;

  const reset = () => setSelected(new Set());
  const toggle = (id: number) =>
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });

  const targetIDs = single ? [single.id] : [...selected];
  const valid = targetIDs.length >= 1;
  const onSplit = () => {
    if (!valid) return;
    split.mutate(
      { book_ids: targetIDs },
      {
        onSuccess: () => {
          reset();
          setOpen(false);
        },
      },
    );
  };

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
            {single ? (
              <>Издание ниже будет вынесено в новую отдельную книгу. Якорное издание остаётся как «{workTitle}».</>
            ) : (
              <>Отметьте издания, которые на самом деле ДРУГАЯ книга, — они будут вынесены в новую книгу. Якорное издание (с названием работы) выносить нельзя, оно остаётся в «{workTitle}».</>
            )}
          </DialogDescription>
        </div>

        {/* Якорь — залочен, не выбирается. */}
        {anchor ? (
          <div className="flex items-center gap-2 rounded-md border border-dashed border-border px-3 py-2 text-sm text-muted-foreground">
            <Lock className="size-4 shrink-0" aria-hidden />
            <span className="flex min-w-0 flex-1 flex-col">
              <span className="truncate">{editionTitle(anchor)}</span>
              <span className="truncate text-xs">Якорное — остаётся в работе</span>
            </span>
          </div>
        ) : null}

        {single ? (
          <div className="rounded-md border border-foreground bg-accent/40 px-3 py-2 text-sm">
            <p className="text-xs font-medium text-muted-foreground">Будет вынесено:</p>
            <p className="truncate">{editionTitle(single)}</p>
            <p className="truncate text-xs text-muted-foreground">{editionMeta(single)}</p>
          </div>
        ) : (
          <ul className="max-h-[50vh] space-y-1 overflow-y-auto py-1">
            {nonAnchors.map((e) => {
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
        )}

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
            Вынести{!single && selected.size >= 1 ? ` (${selected.size})` : ''}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
