/**
 * AuthorTimelinePage — полный таймлайн «жизнь и книги» (/authors/$id/timeline).
 *
 * Два представления: «Главы» (основное: главы жизни как секции, книги периода
 * у заголовка, вехи строками с явными чипами связанных книг) и «Ось»
 * (пропорциональная вертикаль: точки вех на оси, ленты периодов вех и
 * написания по своим дорожкам, подписи книг с L-коннекторами; в узком
 * контейнере — потоковый список с пропорциональными отступами). Minor-вехи
 * и исторический фон — тумблерами, по умолчанию major без фона.
 *
 * Админ в режиме правки скрывает веху (EyeOff); скрытые видны ему
 * зачёркнутыми с кнопкой «вернуть».
 */
import { useState, type ReactNode } from 'react';
import { Link, useParams } from '@tanstack/react-router';
import { Eye, EyeOff, History } from 'lucide-react';
import { BookCover } from '@/components/BookCover';
import { CardToolbar } from '@/components/EditModeToggle';
import { EventIcon, EventText, WorkChip } from '@/components/BioTimeline';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Switch } from '@/components/ui/switch';
import {
  endYear,
  fmtPeriod,
  isPeriod,
  lifeYears,
  pluralEvents,
  useBioTimeline,
  useSetBioEventHidden,
  yearLabel,
  yearOf,
  type BioEvent,
  type BioTimeline,
  type BioWork,
  type ContextEvent,
} from '@/lib/bioTimeline';
import { useAuthor, type YearCount } from '@/lib/catalog';
import { useCanEdit } from '@/lib/editMode';
import { pluralBooks } from '@/lib/format';
import { useContainerWidth } from '@/lib/useContainerWidth';
import { cn } from '@/lib/utils';

type View = 'chapters' | 'axis';

export function AuthorTimelinePage() {
  const { id } = useParams({ strict: false }) as { id: string };
  const author = useAuthor(id);
  const timeline = useBioTimeline(id);
  const canEdit = useCanEdit();
  const [view, setView] = useState<View>('chapters');
  const [showMinor, setShowMinor] = useState(false);
  const [showContext, setShowContext] = useState(false);

  if (timeline.isLoading || author.isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-6 w-40" />
        <Skeleton className="h-64 w-full" />
      </div>
    );
  }
  const tl = timeline.data;
  const a = author.data;
  if (!tl?.available || !tl.events || !tl.born || !a) {
    return (
      <div className="space-y-4">
        <CardToolbar />
        <p className="text-sm text-muted-foreground">
          У этого автора нет таймлайна.{' '}
          <Link to="/authors/$id" params={{ id }} className="underline">
            К карточке автора
          </Link>
        </p>
      </div>
    );
  }
  const visibleTotal = tl.events.filter((e) => !e.hidden).length;
  const bookCount = (a.year_stats ?? []).reduce((n, y) => n + y.count, 0);
  return (
    <article className="space-y-5 @container" data-testid="author-timeline">
      <CardToolbar />
      <header className="space-y-1">
        <Link to="/authors/$id" params={{ id }} className="text-sm text-muted-foreground hover:underline">
          ← {a.full_name}
        </Link>
        <h1 className="text-2xl font-semibold tracking-tight">Жизнь и книги</h1>
        <p className="text-sm text-muted-foreground tabular-nums">
          {lifeYears(tl.born, tl.died)} · {visibleTotal} {pluralEvents(visibleTotal)}
          {bookCount ? ` · ${bookCount} ${pluralBooks(bookCount)} в каталоге` : ''}
        </p>
      </header>
      <div className="flex flex-wrap items-center gap-x-5 gap-y-2 text-sm">
        <div className="flex gap-1" role="group" aria-label="Представление">
          <Button size="sm" variant={view === 'chapters' ? 'default' : 'outline'} onClick={() => setView('chapters')}>
            Главы
          </Button>
          <Button size="sm" variant={view === 'axis' ? 'default' : 'outline'} onClick={() => setView('axis')}>
            Ось
          </Button>
        </div>
        <label className="flex items-center gap-2">
          <Switch checked={showMinor} onCheckedChange={setShowMinor} /> Второстепенные
        </label>
        {tl.context?.length ? (
          <label className="flex items-center gap-2">
            <Switch checked={showContext} onCheckedChange={setShowContext} /> Исторический фон
          </label>
        ) : null}
      </div>
      {view === 'chapters' ? (
        <TimelineChapters tl={tl} yearStats={a.year_stats ?? []} showMinor={showMinor} showContext={showContext} canEdit={canEdit} />
      ) : (
        <TimelineAxis tl={tl} showMinor={showMinor} />
      )}
      <p className="text-xs text-muted-foreground">
        По материалам{' '}
        {tl.sources?.length ? (
          <a href={tl.sources[0].url} className="underline" target="_blank" rel="noreferrer">
            Википедии
          </a>
        ) : (
          'Википедии'
        )}{' '}
        (CC BY-SA 4.0) и Wikidata (CC0). Формулировки — skriptes; интерпретаций нет.
      </p>
    </article>
  );
}

/* ───────────── Главы ───────────── */

type Row = { kind: 'event'; e: BioEvent } | { kind: 'ctx'; c: ContextEvent };

function TimelineChapters({
  tl,
  yearStats,
  showMinor,
  showContext,
  canEdit,
}: {
  tl: BioTimeline;
  yearStats: YearCount[];
  showMinor: boolean;
  showContext: boolean;
  canEdit: boolean;
}) {
  const setHidden = useSetBioEventHidden();
  const events = tl.events ?? [];
  const works = tl.works ?? [];
  const byRef = new Map(works.map((w) => [w.ref, w]));
  const chapters = tl.chapters?.length
    ? tl.chapters
    : [{ key: 'all', title: 'Жизнь', from: yearOf(tl.born!), to: tl.died ? yearOf(tl.died) : 9999 }];
  const toggle = (e: BioEvent) => {
    if (!tl.qid) return;
    setHidden.mutate({ qid: tl.qid, key: e.key, hidden: !e.hidden });
  };
  return (
    <div className="space-y-8">
      {chapters.map((ch) => {
        const inChapter = (y: number) => y >= ch.from && y <= ch.to;
        const evs = events.filter((e) => {
          const y = yearOf(e.date.from);
          const here = e.chapter ? e.chapter === ch.key : inChapter(y);
          if (!here) return false;
          if (e.hidden) return canEdit;
          return showMinor || e.tier === 'major';
        });
        const ctx = showContext ? (tl.context ?? []).filter((c) => inChapter(c.from)) : [];
        const rows: Row[] = [...evs.map((e): Row => ({ kind: 'event', e })), ...ctx.map((c): Row => ({ kind: 'ctx', c }))].sort(
          (x, y) => (x.kind === 'event' ? yearOf(x.e.date.from) : x.c.from) - (y.kind === 'event' ? yearOf(y.e.date.from) : y.c.from),
        );
        const books = yearStats.filter((y) => inChapter(y.year)).flatMap((y) => (y.books ?? []).map((b) => ({ ...b, year: y.year })));
        if (rows.length === 0 && books.length === 0) return null;
        return (
          <section key={ch.key} className="space-y-3" data-testid="timeline-chapter">
            <header className="flex items-baseline justify-between gap-3 border-b border-border pb-1.5">
              <h3 className="text-base font-medium">{ch.title}</h3>
              <span className="text-sm tabular-nums text-muted-foreground">
                {ch.from}–{ch.to}
              </span>
            </header>
            {books.length > 0 ? (
              <div className="flex flex-wrap gap-3">
                {books.map((b) => (
                  <Link key={b.id} to="/books/$id" params={{ id: String(b.id) }} className="flex items-center gap-2 hover:underline">
                    <BookCover title={b.title} placeholder="mini" className="w-7" />
                    <div className="leading-tight">
                      <div className="text-sm">{b.title}</div>
                      <div className="text-xs tabular-nums text-muted-foreground">{b.year}</div>
                    </div>
                  </Link>
                ))}
              </div>
            ) : null}
            <ol className="divide-y divide-border/40">
              {rows.map((r) => {
                if (r.kind === 'ctx') {
                  return (
                    <li key={r.c.key} className="grid grid-cols-[4.5rem_1fr] gap-x-3 py-1 text-xs italic text-muted-foreground/70">
                      <span className="tabular-nums">{r.c.to ? `${r.c.from}–${String(r.c.to).slice(2)}` : r.c.from}</span>
                      <span className="flex items-center gap-1.5">
                        <History className="size-3.5" aria-hidden /> {r.c.short}
                      </span>
                    </li>
                  );
                }
                const e = r.e;
                const minor = e.tier === 'minor';
                return (
                  <li key={e.key} className="group grid grid-cols-[4.5rem_1fr] gap-x-3 py-1.5 @sm:grid-cols-[4.5rem_1fr_auto]" data-testid="timeline-event">
                    <span className={cn('pt-0.5 text-sm tabular-nums', minor ? 'text-muted-foreground/70' : 'text-muted-foreground')}>{yearLabel(e)}</span>
                    <div className="min-w-0">
                      <div className="flex items-start gap-2">
                        <EventIcon type={e.type} className={cn('mt-1 size-4 shrink-0', minor ? 'text-muted-foreground/60' : 'text-muted-foreground')} />
                        <div className="min-w-0">
                          <EventText e={e} />
                          {e.place ? <span className="text-xs text-muted-foreground"> · {e.place.label}</span> : null}
                          {e.works?.map((l) =>
                            l.note ? (
                              <p key={l.ref} className="text-xs italic text-muted-foreground text-pretty">
                                {byRef.get(l.ref)?.title ?? l.ref}: {l.note}
                              </p>
                            ) : null,
                          )}
                        </div>
                        {canEdit ? (
                          <button
                            type="button"
                            onClick={() => toggle(e)}
                            disabled={setHidden.isPending}
                            className={cn(
                              'shrink-0 rounded p-1 text-muted-foreground hover:bg-muted',
                              e.hidden ? 'opacity-100' : 'opacity-0 group-hover:opacity-100 focus:opacity-100',
                            )}
                            aria-label={e.hidden ? 'Вернуть веху' : 'Скрыть веху'}
                          >
                            {e.hidden ? <Eye className="size-4" /> : <EyeOff className="size-4" />}
                          </button>
                        ) : null}
                      </div>
                    </div>
                    <div className="col-start-2 flex flex-wrap items-start gap-1 pt-1 @sm:col-start-3 @sm:pt-0.5">
                      {e.works?.map((l) => {
                        const wk = byRef.get(l.ref);
                        return wk ? <WorkChip key={l.ref} w={wk} /> : null;
                      })}
                    </div>
                  </li>
                );
              })}
            </ol>
          </section>
        );
      })}
    </div>
  );
}

/* ───────────── Ось ───────────── */

const PX_PER_YEAR = 26;

function TimelineAxis({ tl, showMinor }: { tl: BioTimeline; showMinor: boolean }) {
  const [wrapRef, width] = useContainerWidth<HTMLDivElement>();
  const narrow = width > 0 && width < 640;
  const born = yearOf(tl.born!);
  const died = tl.died ? yearOf(tl.died) : new Date().getFullYear();
  const works = (tl.works ?? []).filter((w) => w.written);
  const byRef = new Map((tl.works ?? []).map((w) => [w.ref, w]));
  const events = (tl.events ?? []).filter((e) => !e.hidden && (showMinor || e.tier === 'major'));

  const eventRow = (e: BioEvent, side: 'left' | 'right') => {
    const minor = e.tier === 'minor';
    return (
      <div className={cn('flex items-start gap-2', side === 'left' && 'flex-row-reverse text-right')}>
        <EventIcon type={e.type} className={cn('mt-0.5 size-4 shrink-0', minor ? 'text-muted-foreground/60' : 'text-muted-foreground')} />
        <div className="min-w-0 leading-snug">
          <EventText e={e} className={minor ? 'text-sm' : 'text-[15px]'} />
          {e.works?.length ? (
            <span className="ml-1 text-xs text-muted-foreground">
              → {e.works.map((l) => byRef.get(l.ref)?.title ?? l.ref).join(', ')}
            </span>
          ) : null}
        </div>
      </div>
    );
  };
  const workRow = (w: BioWork) => {
    const inner = (
      <>
        <BookCover title={w.title} placeholder="mini" className="w-7" />
        <div className="leading-tight">
          <div className="text-sm">{w.title}</div>
          <div className="text-xs tabular-nums text-muted-foreground">{fmtPeriod(w.written!)}</div>
        </div>
      </>
    );
    return w.work_id ? (
      <Link to="/works/$id" params={{ id: String(w.work_id) }} className="flex items-center gap-2 hover:underline">
        {inner}
      </Link>
    ) : (
      <div className="flex items-center gap-2">{inner}</div>
    );
  };

  if (narrow) {
    type Item = { key: string; year: number; node: ReactNode; minor: boolean };
    const items: Item[] = [
      ...events.map((e) => ({ key: e.key, year: yearOf(e.date.from), node: eventRow(e, 'right'), minor: e.tier === 'minor' })),
      ...works.map((w) => ({ key: `w-${w.ref}`, year: w.written!.from, node: workRow(w), minor: false })),
    ].sort((x, y) => x.year - y.year);
    let prevYear = born;
    let nextDecade = Math.ceil(born / 10) * 10;
    const rows: ReactNode[] = [];
    for (const it of items) {
      while (nextDecade <= it.year) {
        rows.push(
          <li key={`dec-${nextDecade}`} className="relative -ml-10 mb-1 mt-3 text-[11px] tabular-nums text-muted-foreground">
            <span className="absolute left-10 right-0 top-1/2 h-px bg-border/40" />
            <span className="relative bg-background pr-1">{nextDecade}</span>
          </li>,
        );
        nextDecade += 10;
      }
      const gap = Math.min(96, Math.max(6, (it.year - prevYear) * 8));
      prevYear = it.year;
      rows.push(
        <li key={it.key} className="relative" style={{ marginTop: gap }}>
          <span className="absolute -left-[1.3rem] top-2 size-2 rounded-full bg-foreground" style={{ opacity: it.minor ? 0.4 : 0.9 }} />
          {it.node}
        </li>,
      );
    }
    return (
      <div ref={wrapRef} data-testid="timeline-axis">
        <ol className="ml-10 border-l border-border pl-4">{rows}</ol>
      </div>
    );
  }

  const top = 12;
  const y = (year: number) => top + (year - born) * PX_PER_YEAR;
  const axis = '50%';
  const laneL = `calc(${axis} - 0.75rem)`;
  const laneR = `calc(${axis} + 0.75rem)`;
  const connX = `calc(${axis} + 1.25rem)`;
  const textR = `calc(${axis} + 1.9rem)`;
  const stack = <T,>(items: Array<{ it: T; year: number }>, gap: number) => {
    let last = -Infinity;
    return items
      .sort((p, q) => p.year - q.year)
      .map(({ it, year }) => {
        const yy = Math.max(y(year), last + gap);
        last = yy;
        return { it, y: yy };
      });
  };
  const left = stack(events.map((e) => ({ it: e, year: yearOf(e.date.from) })), 24);
  const right = stack(works.map((w) => ({ it: w, year: w.written!.from })), 44);
  const bottom = Math.max(y(died) + 40, ...left.map((p) => p.y + 40), ...right.map((p) => p.y + 60));
  const decades: number[] = [];
  for (let d = Math.ceil(born / 10) * 10; d <= died; d += 10) decades.push(d);
  const periods = events.filter(isPeriod);

  return (
    <div ref={wrapRef} className="relative" style={{ height: bottom }} data-testid="timeline-axis">
      {decades.map((d) => (
        <div key={d} className="absolute left-0 right-0 border-t border-border/40" style={{ top: y(d) }}>
          <span className="absolute -top-2 left-0 bg-background pr-1 text-[11px] tabular-nums text-muted-foreground">{d}</span>
        </div>
      ))}
      <div className="absolute inset-y-0 w-px bg-border" style={{ left: axis }} />
      {periods.map((e) => (
        <div
          key={`p-${e.key}`}
          className="absolute w-[3px] rounded-full bg-foreground/35"
          style={{ left: laneL, top: y(yearOf(e.date.from)), height: Math.max(4, (endYear(e) - yearOf(e.date.from)) * PX_PER_YEAR) }}
          title={`${yearLabel(e)} · ${e.short}`}
        />
      ))}
      {events.map((e) => (
        <span
          key={`d-${e.key}`}
          className="absolute size-2 -translate-x-1/2 -translate-y-1/2 rounded-full bg-foreground"
          style={{ left: axis, top: y(yearOf(e.date.from)) + 2, opacity: e.tier === 'minor' ? 0.4 : 0.9 }}
        />
      ))}
      {works.map((w) => (
        <div
          key={`w-${w.ref}`}
          className="absolute w-[3px] rounded-full bg-foreground/70"
          style={{ left: laneR, top: y(w.written!.from), height: Math.max(6, (w.written!.to - w.written!.from + 0.35) * PX_PER_YEAR) }}
          title={`${w.title} · ${fmtPeriod(w.written!)}`}
        />
      ))}
      {left.map(({ it: e, y: yy }) => (
        <div key={e.key} className="absolute left-0" style={{ top: yy - 2, right: `calc(${axis} + 1.4rem)` }}>
          {eventRow(e, 'left')}
        </div>
      ))}
      {right.map(({ it: w, y: yy }) => {
        const bandY = y(w.written!.from) + 3;
        const labelY = yy + 12;
        return (
          <div key={`wl-${w.ref}`}>
            <span className="absolute w-px bg-border" style={{ left: connX, top: Math.min(bandY, labelY), height: Math.abs(labelY - bandY) + 1 }} />
            <span className="absolute h-px bg-border" style={{ left: laneR, width: '0.5rem', top: bandY }} />
            <span className="absolute h-px bg-border" style={{ left: connX, width: '0.65rem', top: labelY }} />
            <div className="absolute right-0" style={{ left: textR, top: yy - 2 }}>
              {workRow(w)}
            </div>
          </div>
        );
      })}
    </div>
  );
}
