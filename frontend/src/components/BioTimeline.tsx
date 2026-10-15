/**
 * Общие кирпичи био-таймлайна: иконки типов, формулировка вехи (short с
 * тултипом/раскрытием long), чип книги, полоска-индекс «книги сверху, вехи
 * снизу» с подсветкой связей. Используются тизером на карточке автора,
 * блоком на карточке книги и полной страницей.
 *
 * Монохром (грабля №9): подсветка — контраст и размер, не цвет. Тон —
 * нейтральная летопись.
 */
import { useState } from 'react';
import { Link } from '@tanstack/react-router';
import {
  Baby,
  BookOpen,
  Briefcase,
  Church,
  Circle,
  CircleDot,
  Coins,
  GraduationCap,
  Heart,
  Landmark,
  Lock,
  MapPin,
  Moon,
  PenLine,
  Stethoscope,
  Swords,
  Users,
} from 'lucide-react';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { useContainerWidth } from '@/lib/useContainerWidth';
import {
  endYear,
  fmtPeriod,
  isPeriod,
  relatedKeys,
  yearLabel,
  yearOf,
  type ActiveKey,
  type BioEvent,
  type BioWork,
  type EventType,
} from '@/lib/bioTimeline';
import { cn } from '@/lib/utils';

/** Иконки типов — нейтральные: утрата — убывающая луна, не череп. */
const TYPE_ICON: Record<EventType, typeof Circle> = {
  birth: Circle,
  death: CircleDot,
  loss: Moon,
  love: Heart,
  child: Baby,
  family: Users,
  illness: Stethoscope,
  war: Swords,
  persecution: Lock,
  relocation: MapPin,
  poverty: Coins,
  faith: Church,
  education: GraduationCap,
  career: Briefcase,
  creation: PenLine,
  other: Landmark,
};

export function EventIcon({ type, className }: { type: EventType; className?: string }) {
  const Icon = TYPE_ICON[type] ?? Landmark;
  return <Icon className={className} aria-hidden />;
}

/**
 * Короткая формулировка; long — тултип (десктоп) и раскрытие по клику/тапу
 * (мобайл). Major — обычный контраст, minor — приглушённо.
 *
 * oneLine — для раскладки с фиксированным шагом (ось, #519): строго одна
 * строка с многоточием, без раскрытия по клику (оно легло бы на соседнюю
 * подпись); полный short и long — в тултипе.
 */
export function EventText({ e, className, oneLine = false }: { e: BioEvent; className?: string; oneLine?: boolean }) {
  const [open, setOpen] = useState(false);
  const minor = e.tier === 'minor';
  const detail = [e.long, e.place?.label].filter(Boolean).join(' · ');
  const text = (
    <button
      type="button"
      onClick={() => !oneLine && detail && setOpen((v) => !v)}
      className={cn(
        'text-left',
        oneLine ? 'block max-w-full truncate' : 'text-pretty',
        minor ? 'text-sm text-muted-foreground' : 'text-[15px] text-foreground',
        e.hidden && 'line-through opacity-60',
        className,
      )}
    >
      {e.short}
    </button>
  );
  const tip = oneLine ? (
    <>
      <span className="block">{e.short}</span>
      {detail ? <span className="mt-1 block opacity-80">{detail}</span> : null}
    </>
  ) : (
    detail
  );
  return (
    <span className="min-w-0">
      {tip ? (
        <Tooltip>
          <TooltipTrigger asChild>{text}</TooltipTrigger>
          <TooltipContent className="max-w-xs text-pretty">{tip}</TooltipContent>
        </Tooltip>
      ) : (
        text
      )}
      {open && detail ? <span className="block text-sm text-muted-foreground text-pretty">{detail}</span> : null}
    </span>
  );
}

/** Чип работы: ссылка на карточку, если работа сопоставлена с каталогом. */
export function WorkChip({ w, className }: { w: BioWork; className?: string }) {
  const inner = (
    <>
      <BookOpen className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
      <span className="truncate">{w.title}</span>
      {w.written ? <span className="tabular-nums text-muted-foreground">{fmtPeriod(w.written)}</span> : null}
    </>
  );
  const cls = cn(
    'inline-flex max-w-full items-center gap-1.5 rounded-md border border-border bg-muted/40 px-1.5 py-0.5 text-xs',
    w.work_id && 'hover:bg-muted',
    className,
  );
  return w.work_id ? (
    <Link to="/works/$id" params={{ id: String(w.work_id) }} className={cls}>
      {inner}
    </Link>
  ) : (
    <span className={cls}>{inner}</span>
  );
}

/**
 * Полоска-индекс: ось жизни, книги (периоды написания) сверху, major-вехи
 * снизу; hover подсвечивает элемент и связанные с ним, остальное притушено.
 */
export function LifeStrip({
  born,
  died,
  events,
  works,
  active,
  onActive,
  className,
}: {
  born: string;
  died?: string;
  events: BioEvent[];
  works: BioWork[];
  active: ActiveKey;
  onActive: (k: ActiveKey) => void;
  className?: string;
}) {
  const [ref, w] = useContainerWidth<HTMLDivElement>();
  const b = yearOf(born);
  const d = died ? yearOf(died) : new Date().getFullYear();
  const span = Math.max(1, d - b);
  const pad = 14;
  const h = 64;
  const base = 36;
  const x = (y: number) => pad + ((y - b) / span) * Math.max(0, w - 2 * pad);
  const decades: number[] = [];
  for (let y = Math.ceil(b / 10) * 10; y <= d; y += 10) decades.push(y);
  const majors = events.filter((e) => e.tier === 'major' && e.type !== 'birth' && e.type !== 'death' && !e.hidden);
  const related = relatedKeys(events, active);
  const dim = (k: string) => (active && !related.has(k) ? 0.2 : 1);
  const hot = (k: string) => related.has(k);
  const activeLabel = (() => {
    if (!active) return '';
    if (active.startsWith('e:')) {
      const e = events.find((x) => `e:${x.key}` === active);
      return e ? `${yearLabel(e)} · ${e.short}` : '';
    }
    const wk = works.find((x) => `w:${x.ref}` === active);
    return wk ? `${wk.title}${wk.written ? ` · писалась ${fmtPeriod(wk.written)}` : ''}` : '';
  })();
  return (
    <div ref={ref} className={cn('w-full text-foreground', className)} data-testid="life-strip">
      {w > 0 ? (
        <svg width={w} height={h} role="img" aria-label="Книги и вехи жизни по годам" onMouseLeave={() => onActive(null)}>
          <line x1={pad} x2={w - pad} y1={base} y2={base} stroke="currentColor" strokeOpacity={0.25} />
          {decades.map((dec) => (
            <g key={dec}>
              <line x1={x(dec)} x2={x(dec)} y1={base - 3} y2={base + 3} stroke="currentColor" strokeOpacity={0.35} />
              <text x={x(dec)} y={h - 2} textAnchor="middle" fontSize={10} fill="currentColor" fillOpacity={0.55}>
                {dec}
              </text>
            </g>
          ))}
          {majors.map((e) => {
            const k = `e:${e.key}`;
            const common = {
              onMouseEnter: () => onActive(k),
              style: { opacity: dim(k), transition: 'opacity 120ms', cursor: 'default' as const },
            };
            return isPeriod(e) ? (
              <line
                key={e.key}
                x1={x(yearOf(e.date.from))}
                x2={x(endYear(e))}
                y1={base + 9}
                y2={base + 9}
                stroke="currentColor"
                strokeOpacity={hot(k) ? 1 : 0.5}
                strokeWidth={hot(k) ? 5 : 3}
                strokeLinecap="round"
                {...common}
              />
            ) : (
              <circle key={e.key} cx={x(yearOf(e.date.from))} cy={base + 9} r={hot(k) ? 4.5 : 3} fill="currentColor" fillOpacity={hot(k) ? 1 : 0.7} {...common} />
            );
          })}
          {works
            .filter((wk) => wk.written)
            .map((wk) => {
              const k = `w:${wk.ref}`;
              const from = x(wk.written!.from);
              const to = x(wk.written!.to);
              return (
                <rect
                  key={wk.ref}
                  x={from - 2}
                  width={Math.max(4, to - from + 4)}
                  y={hot(k) ? base - 19 : base - 16}
                  height={hot(k) ? 15 : 12}
                  rx={2}
                  fill="currentColor"
                  fillOpacity={hot(k) ? 1 : 0.85}
                  onMouseEnter={() => onActive(k)}
                  style={{ opacity: dim(k), transition: 'opacity 120ms', cursor: 'default' }}
                />
              );
            })}
        </svg>
      ) : null}
      <p className="h-4 truncate text-xs text-muted-foreground">{activeLabel}</p>
    </div>
  );
}
