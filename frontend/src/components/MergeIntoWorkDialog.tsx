import { useState } from 'react';
import { GitMerge, Search } from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
  DialogTrigger,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { useMergeWorks } from '@/lib/admin';
import { useMe } from '@/lib/auth';
import { useDebouncedValue } from '@/lib/useDebouncedValue';
import { useSuggest } from '@/lib/suggest';
import { cn } from '@/lib/utils';

/**
 * MergeIntoWorkDialog — merge с карточки книги (admin): найти ДРУГУЮ книгу
 * (через тот же typeahead, что Cmd+K — он отдаёт работы) и присоединить её
 * издания к текущей. Целью merge передаётся ТЕКУЩАЯ работа (`target: workId`),
 * чтобы она выжила и URL карточки не сломался. Не-админам не рендерится.
 *
 * Дополняет MergeSuggestions/MergeWorksDialog (серия/автор): покрывает случай,
 * когда дубль вне общей серии/автора и подсказка его не нашла.
 */
export function MergeIntoWorkDialog({ workId, workTitle }: { workId: number; workTitle: string }) {
  const { data: me } = useMe();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const debounced = useDebouncedValue(query, 200);
  // Запрос только при открытом диалоге и ≥2 символах (useSuggest сам гейтит длину).
  const { data, isFetching } = useSuggest(open ? debounced : '', 8);
  const merge = useMergeWorks();

  if (me?.role !== 'admin' || !workId) return null;

  // Исключаем саму текущую работу из результатов.
  const results = (data?.books ?? []).filter((b) => (b.work_id ?? b.id) !== workId);

  const onPick = (targetId: number) => {
    merge.mutate(
      { work_ids: [workId, targetId], target: workId },
      {
        onSuccess: () => {
          setOpen(false);
          setQuery('');
        },
      },
    );
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (!o) setQuery('');
      }}
    >
      <DialogTrigger asChild>
        <Button variant="ghost" size="sm" className="gap-1 text-muted-foreground">
          <GitMerge className="size-4" aria-hidden />
          Объединить с другой книгой…
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-lg">
        <div className="space-y-1">
          <DialogTitle>Объединить с другой книгой</DialogTitle>
          <DialogDescription>
            Найдите книгу, которая на самом деле то же произведение, что «{workTitle}». Её издания
            переедут сюда (текущая книга останется).
          </DialogDescription>
        </div>
        <div className="relative">
          <Search
            className="pointer-events-none absolute left-2 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
            aria-hidden
          />
          <Input
            autoFocus
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Поиск книги…"
            className="pl-8"
            aria-label="Поиск книги для объединения"
          />
        </div>
        <ul className="max-h-[50vh] space-y-1 overflow-y-auto">
          {debounced.trim().length < 2 ? (
            <li className="px-1 py-2 text-xs text-muted-foreground">Введите минимум 2 символа.</li>
          ) : results.length === 0 ? (
            <li className="px-1 py-2 text-xs text-muted-foreground">
              {isFetching ? 'Поиск…' : 'Ничего не найдено.'}
            </li>
          ) : (
            results.map((b) => {
              const tid = b.work_id ?? b.id;
              return (
                <li key={b.id}>
                  <button
                    type="button"
                    disabled={merge.isPending}
                    onClick={() => onPick(tid)}
                    className={cn(
                      'flex w-full flex-col items-start gap-0.5 rounded-md border border-border px-3 py-2 text-left text-sm transition hover:bg-accent/30 disabled:opacity-50',
                    )}
                  >
                    <span className="min-w-0 max-w-full truncate font-medium">{b.title}</span>
                    {b.authors && b.authors.length > 0 ? (
                      <span className="min-w-0 max-w-full truncate text-xs text-muted-foreground">
                        {b.authors.join(', ')}
                        {b.year ? ` · ${b.year}` : ''}
                      </span>
                    ) : null}
                  </button>
                </li>
              );
            })
          )}
        </ul>
      </DialogContent>
    </Dialog>
  );
}
