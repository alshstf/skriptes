import { useState } from 'react';
import { Layers } from 'lucide-react';
import { useIsAdmin } from '@/lib/editMode';
import { Button } from '@/components/ui/button';
import { Callout } from '@/components/ui/callout';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';
import { useMergeWorks, useRejectMerge } from '@/lib/admin';
import { computeMergeSuggestions, type BookListItem, type MergeSuggestion } from '@/lib/books';
import { pluralRu } from '@/lib/format';

/**
 * MergeSuggestions — админ-подсказки на карточке серии/автора.
 *
 * Каталог уже схлопывает издания по работе (одна строка = одна работа), поэтому
 * если у одного `ser_no` оказалось ≥2 строк — это, скорее всего, один том серии
 * в нескольких изданиях/переводах, не слившийся группировкой (например, два
 * по-разному названных перевода без `<src-title-info>`).
 *
 * Одна сводная плашка на все такие тома (#517: у больших серий страница
 * начиналась со стены подсказок) с кнопкой «Посмотреть», жёстко справа. В окне —
 * все группы: «Объединить» или «Не объединять» (отказ запоминается на сервере —
 * rejections, — и подсказка не возвращается). «Объединить все» нет: слияние по
 * эвристике без глаз опасно.
 *
 * Read-only эвристика по УЖЕ загруженным книгам — без отдельного запроса
 * (точностный гейт по src нужен только авто-Tier-1.5; здесь финальное решение
 * принимает админ глазами). Не-админам не рендерится.
 */
export function MergeSuggestions({
  books,
  rejections,
}: {
  books: BookListItem[];
  rejections?: [number, number][];
}) {
  // Подсказка о проблеме (как дубли авторов) — админу и без режима правки (#444).
  const canEdit = useIsAdmin();
  const [open, setOpen] = useState(false);
  // Решённые в окне группы прячутся сразу, не дожидаясь перезапроса карточки.
  const [resolved, setResolved] = useState<Set<string>>(new Set());

  if (!canEdit) return null;

  const groups = computeMergeSuggestions(books, rejections).filter((g) => !resolved.has(groupKey(g)));
  if (groups.length === 0) return null;

  const text =
    groups.length === 1
      ? `Похоже, том #${groups[0].serNo} — одна книга в ${groups[0].books.length} изданиях, не слившихся в одну.`
      : `Похоже, ${groups.length} ${pluralRu(groups.length, ['том', 'тома', 'томов'])} серии изданы в нескольких вариантах, не слившихся в одну книгу.`;

  return (
    <>
      <Callout icon={<Layers className="mt-0.5 size-4 shrink-0" aria-hidden />}>
        <div className="flex items-center gap-3" data-testid="merge-suggestions">
          <span className="min-w-0 flex-1">{text}</span>
          <Button variant="secondary" size="sm" className="shrink-0" onClick={() => setOpen(true)}>
            Посмотреть
          </Button>
        </div>
      </Callout>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-lg">
          <div className="space-y-1">
            <DialogTitle>Возможно, одна книга в нескольких изданиях</DialogTitle>
            <DialogDescription>
              У одного номера тома — несколько разных книг. «Объединить» сделает их одной книгой с
              несколькими изданиями (отменить — «Разделить» на карточке книги); «Не объединять» запомнит,
              что это разные книги, — подсказка не вернётся.
            </DialogDescription>
          </div>
          <ul className="max-h-[60vh] space-y-3 overflow-y-auto py-1">
            {groups.map((g) => (
              <MergeGroup
                key={groupKey(g)}
                group={g}
                onResolved={() => setResolved((s) => new Set(s).add(groupKey(g)))}
              />
            ))}
          </ul>
        </DialogContent>
      </Dialog>
    </>
  );
}

function groupKey(g: MergeSuggestion): string {
  return [...g.workIds].sort((a, b) => a - b).join(',');
}

function MergeGroup({ group, onResolved }: { group: MergeSuggestion; onResolved: () => void }) {
  const merge = useMergeWorks();
  const reject = useRejectMerge();
  const busy = merge.isPending || reject.isPending;
  return (
    <li className="space-y-2 rounded-md border border-border p-3" data-testid="merge-group">
      <p className="text-sm font-medium tabular-nums">Том #{group.serNo}</p>
      <ul className="space-y-1">
        {group.books.map((b) => (
          <li key={b.id} className="flex items-baseline gap-2 text-sm">
            <span className="min-w-0 flex-1 text-pretty">«{b.title}»</span>
            <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
              {[b.lang, b.edition_count && b.edition_count > 1 ? `${b.edition_count} изд.` : null]
                .filter(Boolean)
                .join(' · ')}
            </span>
          </li>
        ))}
      </ul>
      <div className="flex justify-end gap-2">
        <Button
          variant="ghost"
          size="sm"
          disabled={busy}
          onClick={() => reject.mutate({ work_ids: group.workIds }, { onSuccess: onResolved })}
        >
          Не объединять
        </Button>
        <Button
          variant="secondary"
          size="sm"
          disabled={busy}
          onClick={() => merge.mutate({ work_ids: group.workIds }, { onSuccess: onResolved })}
        >
          Объединить
        </Button>
      </div>
    </li>
  );
}
