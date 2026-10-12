/**
 * AuthorLifeContext — блок «В жизни автора в это время» на карточке книги:
 * 2–4 вехи вокруг периода написания (окно считает бэкенд), пометка связи
 * курсивом, ссылка на полный таймлайн. Нечего показать — секции нет вовсе.
 */
import { Link } from '@tanstack/react-router';
import { Landmark } from 'lucide-react';
import { EventIcon, EventText } from '@/components/BioTimeline';
import { fmtDate, fmtPeriod, relationLabel, useLifeContext } from '@/lib/bioTimeline';

export function AuthorLifeContext({ workId }: { workId: number | undefined }) {
  const { data } = useLifeContext(workId);
  if (!data?.available || !data.items?.length || !data.author) return null;
  return (
    <section className="space-y-2" data-testid="life-context">
      <h3 className="flex flex-wrap items-center gap-x-2 text-sm font-medium">
        <Landmark className="size-4" aria-hidden /> В жизни автора в это время
        {data.period ? <span className="font-normal text-muted-foreground">· писалась в {fmtPeriod(data.period)}</span> : null}
      </h3>
      <ol className="space-y-2">
        {data.items.map((it) => (
          <li key={it.event.key} className="grid grid-cols-1 gap-y-0.5 @sm:grid-cols-[8.5rem_1fr] @sm:gap-x-3">
            <span className="text-xs tabular-nums text-muted-foreground @sm:pt-0.5 @sm:text-sm">{fmtDate(it.event.date)}</span>
            <div className="min-w-0">
              <div className="flex items-start gap-2">
                <EventIcon type={it.event.type} className="mt-1 size-4 shrink-0 text-muted-foreground" />
                <div className="min-w-0">
                  <div className="flex flex-wrap items-baseline gap-x-2">
                    <EventText e={it.event} className="text-[15px] text-foreground" />
                    <span className="text-xs text-muted-foreground">{relationLabel(it.relation)}</span>
                  </div>
                  {it.note ? <p className="text-xs italic text-muted-foreground text-pretty">{it.note}</p> : null}
                </div>
              </div>
            </div>
          </li>
        ))}
      </ol>
      <Link
        to="/authors/$id/timeline"
        params={{ id: String(data.author.id) }}
        className="inline-block text-sm text-muted-foreground hover:underline"
      >
        Вся жизнь и книги →
      </Link>
    </section>
  );
}
