import { Sparkles } from 'lucide-react';
import { useRelatedRecs, workIdOf } from '@/lib/recs';
import { CoverCard } from '@/components/Shelf';
import { RecReasons } from '@/components/RecReason';

/**
 * AfterRatingRecs — «Раз понравилось — вот следующая» (план recsys/02, 2.8):
 * после оценки ≥ 4 на карточке книги — до трёх карточек правил «дальше в
 * цикле» и «автор, которого вы дочитали», основание которых — эта работа.
 * Нечего предложить — ничего не рендерим (без пустых обещаний).
 */
export function AfterRatingRecs({ workId }: { workId: number }) {
  const { data } = useRelatedRecs(workId, true);
  if (!data || data.length === 0) return null;
  return (
    <div className="space-y-2 border-t border-border pt-3" data-testid="after-rating-recs">
      <p className="flex items-center gap-1.5 text-sm font-medium">
        <Sparkles className="size-4 shrink-0" aria-hidden />
        Раз понравилось — вот следующая
      </p>
      <div className="flex gap-4 overflow-x-auto pb-1">
        {data.map((it) => (
          <CoverCard
            key={it.id}
            to={`/works/${workIdOf(it)}`}
            title={it.title}
            authors={it.authors}
            coverPath={it.cover_path}
            coverEditionId={it.id}
          >
            <RecReasons reasons={it.reasons} className="mt-0.5" />
          </CoverCard>
        ))}
      </div>
    </div>
  );
}
