import { useEffect, useMemo, useState } from 'react';
import { Link, useParams } from '@tanstack/react-router';
import { Award, ChevronLeft, ExternalLink } from 'lucide-react';
import { BookListItem } from '@/components/BookListItem';
import { Callout } from '@/components/ui/callout';
import { Switch } from '@/components/ui/switch';
import { groupByYear, useAwards, useAwardWins, yearAnchor, type AwardSummary, type AwardWin } from '@/lib/awards';

/**
 * AwardsPage — /awards: премии из белого списка по разделам (русская
 * литература, русская фантастика, международные) со счётчиком лауреатов,
 * которые есть в библиотеке.
 */
export function AwardsPage() {
  const q = useAwards();
  const groups = useMemo(() => {
    const out: { name: string; items: AwardSummary[] }[] = [];
    for (const a of q.data ?? []) {
      let g = out.find((x) => x.name === a.group);
      if (!g) {
        g = { name: a.group, items: [] };
        out.push(g);
      }
      g.items.push(a);
    }
    return out;
  }, [q.data]);
  const empty = q.data != null && q.data.every((a) => a.wins === 0);

  return (
    <div className="space-y-6">
      <div className="space-y-1">
        <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight">
          <Award className="size-6" aria-hidden />
          Премии
        </h1>
        <p className="text-sm text-pretty text-muted-foreground">
          Лауреаты литературных премий по годам, по данным Фантлаба. Книги и авторы из библиотеки — ссылками.
        </p>
      </div>
      {q.isLoading ? <p className="text-sm italic text-muted-foreground">Загрузка…</p> : null}
      {empty ? (
        <Callout>Лауреаты ещё загружаются — это происходит в фоне через несколько минут после запуска.</Callout>
      ) : null}
      {groups.map((g) => (
        <section key={g.name} className="space-y-2">
          <h2 className="text-sm font-medium text-muted-foreground">{g.name}</h2>
          <ul className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
            {g.items.map((a) => (
              <li key={a.key}>
                <Link
                  to="/awards/$key"
                  params={{ key: a.key }}
                  className="flex h-full flex-col gap-0.5 rounded-md border border-border px-3 py-2.5 transition hover:bg-accent/30"
                >
                  <span className="text-sm font-medium">{a.name}</span>
                  <span className="text-xs tabular-nums text-muted-foreground">
                    {a.wins > 0 ? (
                      <>
                        {yearsRange(a.first_year, a.last_year)} · в библиотеке {a.in_catalog} из {a.wins}
                      </>
                    ) : (
                      'нет данных'
                    )}
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

function yearsRange(first?: number, last?: number): string {
  if (!first || !last) return '';
  return first === last ? String(first) : `${first}–${last}`;
}

/**
 * AwardPage — /awards/$key: лауреаты премии по годам (свежие сверху), внутри
 * года — по номинациям. Книга из библиотеки — карточкой, остальные — строкой
 * со ссылкой на Фантлаб. Книги, скрытые настройками контента, сервер не отдаёт.
 */
export function AwardPage() {
  const { key } = useParams({ strict: false }) as { key: string };
  const q = useAwardWins(key);
  const [onlyLibrary, setOnlyLibrary] = useState(false);
  const wins = useMemo(() => {
    const all = q.data?.wins ?? [];
    return onlyLibrary ? all.filter(inLibrary) : all;
  }, [q.data, onlyLibrary]);
  const years = useMemo(() => groupByYear(wins), [wins]);
  const all = q.data?.wins ?? [];
  const inLib = all.filter(inLibrary).length;
  const decades = useMemo(() => decadeLinks(years.map((y) => y.year)), [years]);

  // Переход с плашки карточки (#y1966): данные приходят позже навигации — к
  // якорю прокручиваем, когда год отрисован.
  useEffect(() => {
    if (!q.data) return;
    const id = window.location.hash.slice(1);
    if (id) document.getElementById(id)?.scrollIntoView();
  }, [q.data]);

  if (q.isLoading) return <p className="text-sm italic text-muted-foreground">Загрузка…</p>;
  if (q.isError || !q.data) return <p className="text-sm text-destructive">Премия не найдена.</p>;
  const award = q.data.award;

  return (
    <div className="space-y-5">
      <div className="space-y-1">
        <Link
          to="/awards"
          className="inline-flex items-center gap-1 text-sm text-muted-foreground transition hover:text-foreground"
        >
          <ChevronLeft className="size-4" aria-hidden />
          Премии
        </Link>
        <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight">
          <Award className="size-6" aria-hidden />
          {award.name}
        </h1>
        <p className="text-sm tabular-nums text-muted-foreground">
          {award.group}
          {all.length > 0 ? ` · в библиотеке ${inLib} из ${all.length}` : ''}
          {award.max_year ? ` · лауреаты по ${award.max_year} год` : ''}
        </p>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <label className="flex items-center gap-2 text-sm">
          <Switch checked={onlyLibrary} onCheckedChange={setOnlyLibrary} aria-label="Только книги из библиотеки" />
          Только из библиотеки
        </label>
        {decades.length > 1 ? (
          <nav className="flex flex-wrap gap-1" aria-label="Годы">
            {decades.map((d) => (
              <a
                key={d.label}
                href={`#${yearAnchor(d.year)}`}
                className="rounded-md px-2 py-0.5 text-xs tabular-nums text-muted-foreground transition hover:bg-accent/40 hover:text-foreground"
              >
                {d.label}
              </a>
            ))}
          </nav>
        ) : null}
      </div>

      {years.length === 0 ? (
        <p className="text-sm italic text-muted-foreground">
          {all.length === 0 ? 'Лауреаты ещё не загружены.' : 'Книг этой премии в библиотеке нет.'}
        </p>
      ) : null}
      {years.map((y) => (
        <section key={y.year} id={yearAnchor(y.year)} className="scroll-mt-20 space-y-2">
          <h2 className="text-lg font-semibold tabular-nums">{y.year}</h2>
          <div className="space-y-3">
            {y.noms.map((n) => (
              <div key={n.nomination} className="space-y-1">
                {n.nomination ? <h3 className="text-xs font-medium text-muted-foreground">{n.nomination}</h3> : null}
                <ul className="divide-y divide-border/60 rounded-md border border-border">
                  {n.wins.map((w) => (
                    <li key={w.id}>
                      <WinRow win={w} />
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        </section>
      ))}
    </div>
  );
}

function inLibrary(w: AwardWin): boolean {
  return w.item != null || w.author_id != null;
}

// decadeLinks — переходы по десятилетиям: якорь — самый свежий год десятилетия.
function decadeLinks(years: number[]): { label: string; year: number }[] {
  const out: { label: string; year: number }[] = [];
  for (const y of years) {
    const d = Math.floor(y / 10) * 10;
    if (!out.some((x) => x.label === `${d}-е`)) out.push({ label: `${d}-е`, year: y });
  }
  return out;
}

function WinRow({ win }: { win: AwardWin }) {
  if (win.item) return <BookListItem book={win.item} />;
  if (win.kind === 'author') {
    return (
      <div className="flex items-center justify-between gap-2 px-3 py-2.5 text-sm">
        {win.author_id ? (
          <Link to="/authors/$id" params={{ id: String(win.author_id) }} className="font-medium hover:underline">
            {win.author}
          </Link>
        ) : (
          <span className="text-muted-foreground">{win.author}</span>
        )}
        {!win.author_id ? <SourceLink win={win} /> : null}
      </div>
    );
  }
  return (
    <div className="flex items-start justify-between gap-2 px-3 py-2.5 text-sm text-muted-foreground">
      <span className="min-w-0 text-pretty">
        <span className="text-foreground/80">{win.title}</span>
        {win.author ? <> — {win.author}</> : null}
      </span>
      <SourceLink win={win} />
    </div>
  );
}

// SourceLink — книги нет в библиотеке: страница произведения на Фантлабе.
function SourceLink({ win }: { win: AwardWin }) {
  if (!win.source_url) return null;
  return (
    <a
      href={win.source_url}
      target="_blank"
      rel="noopener noreferrer"
      className="inline-flex shrink-0 items-center gap-1 text-xs text-muted-foreground transition hover:text-foreground"
      title="Нет в библиотеке — открыть на Фантлабе"
    >
      Фантлаб
      <ExternalLink className="size-3" aria-hidden />
    </a>
  );
}
