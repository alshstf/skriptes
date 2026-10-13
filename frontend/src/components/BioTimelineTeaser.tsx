/**
 * BioTimelineTeaser — свёрнутый таймлайн на карточке автора: полоска-индекс
 * (книги сверху, major-вехи снизу) + до 7 главных вех, прежде всего с линками
 * на книги; hover связывает полоску и список. Полная версия — отдельная
 * страница /authors/$id/timeline. Нет таймлайна — карточки нет.
 */
import { useState } from 'react';
import { Link } from '@tanstack/react-router';
import { Landmark } from 'lucide-react';
import { EventText, LifeStrip, WorkChip } from '@/components/BioTimeline';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { pluralEvents, relatedKeys, teaserEvents, useBioTimeline, yearLabel, type ActiveKey } from '@/lib/bioTimeline';
import { cn } from '@/lib/utils';

export function BioTimelineTeaser({ authorId }: { authorId: number }) {
  const { data } = useBioTimeline(authorId);
  const [active, setActive] = useState<ActiveKey>(null);
  if (!data?.available || !data.events?.length || !data.born) return null;
  const events = data.events;
  const works = data.works ?? [];
  const related = relatedKeys(events, active);
  const shown = teaserEvents(events);
  const byRef = new Map(works.map((w) => [w.ref, w]));
  const total = events.filter((e) => !e.hidden).length;
  return (
    <Card data-testid="bio-timeline-teaser">
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <Landmark className="size-4" aria-hidden /> Жизнь и книги
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3 pt-0">
        <LifeStrip born={data.born} died={data.died} events={events} works={works} active={active} onActive={setActive} />
        <ol className="space-y-0.5" onMouseLeave={() => setActive(null)}>
          {shown.map((e) => {
            const k = `e:${e.key}`;
            return (
              <li
                key={e.key}
                onMouseEnter={() => setActive(k)}
                className={cn(
                  '-mx-1.5 grid grid-cols-[4.5rem_1fr] gap-x-3 rounded-md px-1.5 py-1 transition-opacity',
                  related.has(k) && 'bg-muted/50',
                  active && !related.has(k) && 'opacity-40',
                )}
              >
                <span className="pt-0.5 text-sm tabular-nums text-muted-foreground">{yearLabel(e)}</span>
                <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
                  <EventText e={e} />
                  {e.works?.map((l) => {
                    const wk = byRef.get(l.ref);
                    return wk ? (
                      <span
                        key={l.ref}
                        onMouseEnter={(ev) => {
                          ev.stopPropagation();
                          setActive(`w:${l.ref}`);
                        }}
                      >
                        <WorkChip w={wk} />
                      </span>
                    ) : null;
                  })}
                </div>
              </li>
            );
          })}
        </ol>
        <Button asChild variant="outline" size="sm">
          <Link to="/authors/$id/timeline" params={{ id: String(authorId) }}>
            Открыть таймлайн · {total} {pluralEvents(total)}
          </Link>
        </Button>
      </CardContent>
    </Card>
  );
}
