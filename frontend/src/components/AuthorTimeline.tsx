import { useMemo, useState } from 'react';
import { Link } from '@tanstack/react-router';
import {
  Award,
  BookMarked,
  Baby,
  BookOpen,
  Church,
  Coins,
  GraduationCap,
  Heart,
  Home,
  Landmark,
  MapPin,
  Moon,
  Pencil,
  Stethoscope,
  Swords,
  Lock,
  Circle,
} from 'lucide-react';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import {
  buildTimeline,
  collapseRows,
  relatedYears,
  spansAt,
  pluralYears,
  TIMELINE_GAP_MIN,
  type AuthorEvent,
  type AuthorEventType,
  type EventAttribution,
  type TimelineBooks,
  type TimelineRow,
} from '@/lib/authorEvents';
import { cn } from '@/lib/utils';

/**
 * AuthorTimeline — двусторонний таймлайн «жизнь автора ⟷ его книги».
 *
 * Идея фичи: увидев, что в год написания книги автор потерял ребёнка или
 * вернулся с фронта, читатель смотрит на произведение под новым углом. Связь
 * бывает прямой и контрастной — мы показываем факты рядом, вывод за читателем,
 * никаких интерпретаций (план cryptic-roaming-turing).
 *
 * Раскладка. Десктоп — grid [1fr auto 1fr]: события слева, ось лет по центру,
 * книги справа. Мобила — [auto 1fr]: ось слева, события и книги в одной
 * колонке (двусторонний grid на 375px нечитаем).
 *
 * Тон — нейтральная летопись: трагедии без драматизации (никаких черепов),
 * иконка типа + формулировка + место. Монохром (грабля №9): подсветка связи —
 * bg-muted, не цвет.
 */

/** Иконки типов. Нейтральные: утрата — убывающая луна, а не череп. */
const TYPE_ICON: Record<AuthorEventType, typeof Circle> = {
  birth: Circle,
  death: Circle,
  war: Swords,
  persecution: Lock,
  loss: Moon,
  isolation: Home,
  poverty: Coins,
  spiritual: Church,
  love: Heart,
  child: Baby,
  illness: Stethoscope,
  relocation: MapPin,
  career: BookMarked,
  creation_mode: Pencil,
  education: GraduationCap,
  residence: Home,
  award: Award,
  other: Landmark,
};

/** Сколько строк показываем до «Развернуть» (у классика их под 60). */
const COLLAPSED_ROWS = 12;

export function AuthorTimeline({
  events,
  yearStats,
  attribution,
}: {
  events: AuthorEvent[];
  yearStats: TimelineBooks[];
  attribution?: EventAttribution[];
}) {
  const [expanded, setExpanded] = useState(false);
  const [activeYear, setActiveYear] = useState<number | null>(null);

  const rows = useMemo(() => buildTimeline(events, yearStats), [events, yearStats]);
  const periods = useMemo(() => events.filter((e) => e.year_to != null), [events]);

  if (rows.length === 0) return null;

  const visible = expanded ? rows : collapseRows(rows, COLLAPSED_ROWS);
  const hiddenCount = rows.length - visible.length;
  // Подсвечиваем годы вокруг наведённой книги + периоды, накрывающие её год.
  const highlight = activeYear == null ? [] : relatedYears(activeYear);

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <Landmark className="size-4" aria-hidden /> Жизнь и книги
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3 pt-0">
        <ol className="grid grid-cols-[auto_1fr] gap-x-3 sm:grid-cols-[1fr_auto_1fr] sm:gap-x-4">
          {gridRows(visible).map(({ row, gridRow, gapYears }) => (
            <TimelineRowView
              key={row.year}
              row={row}
              gridRow={gridRow}
              covering={spansAt(periods, row.year)}
              gapYears={gapYears}
              highlighted={highlight.includes(row.year)}
              onBookFocus={setActiveYear}
            />
          ))}
        </ol>

        {hiddenCount > 0 ? (
          <Button variant="ghost" size="sm" className="w-full" onClick={() => setExpanded(true)}>
            Развернуть · ещё {hiddenCount}
          </Button>
        ) : null}

        {attribution?.length ? (
          <p className="text-xs text-muted-foreground">
            Источники:{' '}
            {attribution.map((a, i) => (
              <span key={a.source}>
                {i > 0 ? ' · ' : ''}
                {a.url ? (
                  <a
                    href={a.url}
                    target="_blank"
                    rel="noreferrer"
                    className="underline underline-offset-2 hover:text-foreground"
                  >
                    {sourceLabel(a.source)}
                  </a>
                ) : (
                  sourceLabel(a.source)
                )}{' '}
                ({a.license})
              </span>
            ))}
          </p>
        ) : null}
      </CardContent>
    </Card>
  );
}

function sourceLabel(source: string): string {
  return source === 'wikipedia' ? 'Википедия' : source === 'wikidata' ? 'Wikidata' : source;
}

/**
 * gridRows — раскладка строк по grid-строкам.
 *
 * Каждая логическая строка занимает ДВЕ grid-строки: на мобиле события живут
 * в первой, книги во второй (одна колонка справа от оси), на десктопе обе
 * ячейки садятся в первую (события слева, книги справа), а вторая схлопывается
 * в ноль. Это позволяет рендерить события ОДИН раз вместо двух копий под
 * брейкпоинты — иначе скринридер читает их дважды.
 */
function gridRows(rows: TimelineRow[]): { row: TimelineRow; gridRow: number; gapYears: number }[] {
  let cursor = 1;
  return rows.map((row, i) => {
    const gapYears = i > 0 ? row.year - rows[i - 1].year - 1 : 0;
    const collapsed = i > 0 && gapYears + 1 >= TIMELINE_GAP_MIN;
    if (collapsed) cursor += 1; // строка-разделитель «· · ·»
    const gridRow = cursor;
    cursor += 2;
    return { row, gridRow, gapYears: collapsed ? gapYears : 0 };
  });
}

/**
 * Одна строка оси: события | год | книги. Позиция каждой ячейки задана явно
 * (--r + col-start), поэтому DOM-порядок и брейкпоинт независимы. `data-year`
 * + `data-highlighted` — крючки подсветки связи «книга ↔ что происходило».
 */
function TimelineRowView({
  row,
  gridRow,
  covering,
  gapYears,
  highlighted,
  onBookFocus,
}: {
  row: TimelineRow;
  gridRow: number;
  covering: AuthorEvent[];
  gapYears: number;
  highlighted: boolean;
  onBookFocus: (year: number | null) => void;
}) {
  // ⚠️ grid-row задаём инлайном, а не классами row-start+row-span: Tailwind'ский
  // row-span-* компилируется в ШОРТКАТ grid-row, который затирает
  // grid-row-start из row-start-[…] — ячейки уезжают в авто-поток и ось
  // расходится с событиями (поймано скриншотом).
  return (
    <>
      {gapYears > 0 ? <YearGap years={gapYears} gridRow={gridRow - 1} /> : null}

      {/* События: мобила — колонка 2, строка R; десктоп — колонка 1. */}
      <li
        data-year={row.year}
        data-highlighted={highlighted || undefined}
        style={{ gridRow }}
        className={cn(
          'col-start-2 py-1.5 transition-colors sm:col-start-1 sm:text-right',
          highlighted && 'bg-muted',
        )}
      >
        <EventList events={row.events} />
      </li>

      {/* Ось: год + лента накрывающих периодов. Всегда span 2: на мобиле
          накрывает события и книги, на десктопе вторая строка нулевая. */}
      <li
        style={{ gridRow: `${gridRow} / span 2` }}
        className={cn(
          'relative col-start-1 flex justify-center border-x px-2 py-1.5 text-xs tabular-nums transition-colors sm:col-start-2',
          highlighted ? 'bg-muted font-medium text-foreground' : 'text-muted-foreground',
        )}
        aria-hidden
      >
        {covering.length > 0 ? (
          <span
            data-period-span
            className="absolute inset-y-0 left-1/2 w-px -translate-x-1/2 bg-foreground/25"
            title={covering.map((c) => c.title).join(' · ')}
          />
        ) : null}
        <span className="relative bg-card px-1">{row.year}</span>
      </li>

      {/* Книги: мобила — колонка 2, строка R+1; десктоп — колонка 3, строка R. */}
      <li
        data-year={row.year}
        data-highlighted={highlighted || undefined}
        // --r нужен только здесь: строка книг зависит от брейкпоинта
        // (мобила R+1 под событиями, десктоп R справа от оси).
        style={{ '--r': gridRow } as React.CSSProperties}
        className={cn(
          'col-start-2 row-start-[calc(var(--r)+1)] space-y-1 pb-1.5 transition-colors sm:col-start-3 sm:row-start-[var(--r)] sm:py-1.5',
          highlighted && 'bg-muted',
        )}
        onMouseEnter={() => row.books.length > 0 && onBookFocus(row.year)}
        onMouseLeave={() => onBookFocus(null)}
      >
        {row.books.map((b) => (
          <Link
            key={b.id}
            to="/works/$id"
            params={{ id: String(b.id) }}
            // Иконка книги обязательна на мобиле: там книги идут в одной
            // колонке с событиями и без маркера читаются как ещё одна веха.
            className="flex items-start gap-1.5 text-sm font-medium hover:underline"
            onFocus={() => onBookFocus(row.year)}
            onBlur={() => onBookFocus(null)}
          >
            <BookOpen className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" aria-hidden />
            {b.title}
          </Link>
        ))}
      </li>
    </>
  );
}

/**
 * Схлопнутый разрыв оси: «· · · / 12 лет» прямо на оси вместо дюжины пустых
 * строк. Подпись держим внутри осевой ячейки — вынесенная в колонку книг, она
 * читалась как содержимое года.
 */
function YearGap({ years, gridRow }: { years: number; gridRow: number }) {
  return (
    <li
      style={{ gridRow }}
      className="col-start-1 flex flex-col items-center gap-0.5 border-x py-1 text-muted-foreground/60 sm:col-start-2"
      aria-label={`${pluralYears(years)} без записей`}
    >
      <span className="bg-card px-1 text-[10px] leading-none">· · ·</span>
      <span className="bg-card px-1 text-[10px] leading-none whitespace-nowrap">{pluralYears(years)}</span>
    </li>
  );
}

function EventList({ events }: { events: AuthorEvent[] }) {
  if (events.length === 0) return null;
  return (
    <div className="space-y-1">
      {events.map((ev) => {
        const Icon = TYPE_ICON[ev.type] ?? Circle;
        return (
          <div
            key={ev.id}
            // Десктоп: иконка справа от текста (события «прижаты» к оси).
            className="flex items-start gap-1.5 text-sm sm:flex-row-reverse"
          >
            <Icon className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" aria-hidden />
            <span>
              {ev.title}
              {ev.year_to != null ? (
                <span className="text-muted-foreground"> · до {ev.year_to}</span>
              ) : null}
              {ev.place ? <span className="text-muted-foreground"> · {ev.place}</span> : null}
            </span>
          </div>
        );
      })}
    </div>
  );
}
