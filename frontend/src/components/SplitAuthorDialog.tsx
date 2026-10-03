import { useMemo, useState } from 'react';
import { useNavigate } from '@tanstack/react-router';
import { Check, Split } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Callout } from '@/components/ui/callout';
import { Input } from '@/components/ui/input';
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
  DialogTrigger,
} from '@/components/ui/dialog';
import { useSplitAuthor } from '@/lib/admin';
import { useMe } from '@/lib/auth';
import type { Author } from '@/lib/catalog';
import { authorWorks } from '@/lib/authorSplit';
import { cn } from '@/lib/utils';

/**
 * SplitAuthorDialog — разделить автора (#356, admin): под одним именем в INPX бывают
 * два человека без уточнения («Берг Николай» — поэт XIX века и автор сетевой
 * литературы). Выбранные работы уходят к автору с тем же именем и уточнением; это
 * ручная правка авторов работы — переживает переимпорт и откатывается на карточке
 * книги. Если работы распадаются на две эпохи (author.era_split), над карточкой —
 * подсказка, а в диалоге заранее отмечена старшая группа. Не-админам не рендерится.
 */
export function SplitAuthorDialog({ author }: { author: Author }) {
  const { data: me } = useMe();
  const navigate = useNavigate();
  const split = useSplitAuthor();
  const works = useMemo(() => authorWorks(author), [author]);
  const [open, setOpen] = useState(false);
  const [note, setNote] = useState('');
  const [selected, setSelected] = useState<Set<number>>(new Set());

  if (me?.role !== 'admin' || works.length < 2) return null;

  const hint = author.era_split;
  const openWith = (o: boolean) => {
    setOpen(o);
    if (o) {
      setSelected(new Set(hint?.older.work_ids ?? []));
      setNote('');
    }
  };
  const toggle = (id: number) =>
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });
  const canSubmit = note.trim() !== '' && selected.size > 0 && selected.size < works.length && !split.isPending;
  const onSplit = () => {
    if (!canSubmit) return;
    split.mutate(
      { authorId: author.id, note: note.trim(), work_ids: [...selected] },
      {
        onSuccess: (res) => {
          setOpen(false);
          void navigate({ to: '/authors/$id', params: { id: String(res.author_id) } });
        },
      },
    );
  };

  return (
    <div className="space-y-2 pt-1">
      {hint ? (
        <Callout icon={<Split className="size-3.5" aria-hidden />}>
          Похоже, под этим именем два автора: работы {hint.older.from}–{hint.older.to} (
          {hint.older.work_ids.length}) и {hint.newer.from}–{hint.newer.to} ({hint.newer.work_ids.length}).
        </Callout>
      ) : null}
      <Dialog open={open} onOpenChange={openWith}>
        <DialogTrigger asChild>
          <Button variant="ghost" size="sm" className="gap-1 px-0 text-xs text-muted-foreground">
            <Split className="size-3.5" aria-hidden />
            Разделить автора…
          </Button>
        </DialogTrigger>
        <DialogContent className="max-w-lg">
          <div className="space-y-1">
            <DialogTitle>Разделить автора</DialogTitle>
            <DialogDescription>
              Отмеченные работы перейдут к автору «{author.full_name}» с уточнением — например, «поэт» или
              «фантаст». Если такой автор уже есть, к нему. Био и фото обоих найдутся заново.
            </DialogDescription>
          </div>
          <label className="space-y-1 text-sm" htmlFor="split-note">
            <span className="text-muted-foreground">Уточнение для нового автора</span>
            <Input
              id="split-note"
              value={note}
              maxLength={100}
              placeholder="поэт"
              onChange={(e) => setNote(e.target.value)}
            />
          </label>
          <ul className="max-h-[45vh] space-y-1 overflow-y-auto py-1" aria-label="Работы автора">
            {works.map((w) => {
              const on = selected.has(w.workId);
              return (
                <li key={w.workId}>
                  <button
                    type="button"
                    onClick={() => toggle(w.workId)}
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
                    <span className="w-10 shrink-0 tabular-nums text-muted-foreground">{w.year ?? '—'}</span>
                    <span className="min-w-0 flex-1 truncate">
                      {w.title}
                      {w.series ? <span className="text-muted-foreground"> · {w.series}</span> : null}
                    </span>
                  </button>
                </li>
              );
            })}
          </ul>
          <div className="flex items-center justify-between gap-2">
            <span className="text-xs text-muted-foreground tabular-nums">
              Отмечено {selected.size} из {works.length}
            </span>
            <div className="flex gap-2">
              <Button variant="ghost" size="sm" onClick={() => setOpen(false)}>
                Отмена
              </Button>
              <Button variant="secondary" size="sm" disabled={!canSubmit} onClick={onSplit}>
                Перенести
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
