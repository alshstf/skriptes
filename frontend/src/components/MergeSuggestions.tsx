import { Layers } from 'lucide-react';
import { useIsAdmin } from '@/lib/editMode';
import { Button } from '@/components/ui/button';
import { Callout } from '@/components/ui/callout';
import { useMergeWorks } from '@/lib/admin';
import { computeMergeSuggestions, type BookListItem } from '@/lib/books';

/**
 * MergeSuggestions — админ-подсказки на карточке серии/автора.
 *
 * Каталог уже схлопывает издания по работе (одна строка = одна работа), поэтому
 * если у одного `ser_no` оказалось ≥2 строк — это, скорее всего, один том серии
 * в нескольких изданиях/переводах, не слившийся группировкой (например, два
 * по-разному названных перевода без `<src-title-info>`). Подсказка предлагает
 * объединить их в одну книгу одним кликом.
 *
 * Read-only эвристика по УЖЕ загруженным книгам — без отдельного запроса
 * (точностный гейт по src нужен только авто-Tier-1.5; здесь финальное решение
 * принимает админ глазами). Не-админам не рендерится.
 */
export function MergeSuggestions({ books }: { books: BookListItem[] }) {
  // Подсказка о проблеме (как дубли авторов) — админу и без режима правки (#444).
  const canEdit = useIsAdmin();
  const merge = useMergeWorks();

  if (!canEdit) return null;

  const groups = computeMergeSuggestions(books);
  if (groups.length === 0) return null;

  return (
    <div className="space-y-2">
      {groups.map((g) => (
        <Callout key={g.serNo} icon={<Layers className="mt-0.5 size-4 shrink-0" aria-hidden />}>
          <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1.5">
            <span>
              Похоже, том #{g.serNo} — одна книга в {g.books.length}&nbsp;изданиях:{' '}
              {g.books.map((b, i) => (
                <span key={b.id} className="text-foreground/80">
                  {i > 0 ? ', ' : ''}«{b.title}»
                </span>
              ))}
              .
            </span>
            <Button
              variant="secondary"
              size="sm"
              disabled={merge.isPending}
              onClick={() => merge.mutate({ work_ids: g.workIds })}
            >
              Объединить
            </Button>
          </div>
        </Callout>
      ))}
    </div>
  );
}
