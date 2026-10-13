/**
 * Био-таймлайн автора ⟷ книги (план bio-timeline-v3): типы ответов API,
 * query-хуки и чистые помощники (даты, подписи связей, выбор вех для тизера,
 * подсветка «веха ⟷ книга»).
 *
 * Данные приходят из курируемого датасета (backend/internal/bio); UI ничего
 * не домысливает — только показывает факты и арифметику дат.
 */
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from '@/lib/api';
import { pluralRu } from '@/lib/format';

export type EventType =
  | 'birth'
  | 'death'
  | 'loss'
  | 'love'
  | 'child'
  | 'family'
  | 'illness'
  | 'war'
  | 'persecution'
  | 'relocation'
  | 'poverty'
  | 'faith'
  | 'education'
  | 'career'
  | 'creation'
  | 'other';

export type Tier = 'major' | 'minor';

export type BioDate = { from: string; to?: string; precision: 'year' | 'month' | 'day' };

export type WorkLink = { ref: string; note?: string };

export type BioEvent = {
  key: string;
  date: BioDate;
  type: EventType;
  tier: Tier;
  short: string;
  long?: string;
  place?: { label: string; qid?: string };
  works?: WorkLink[];
  chapter?: string;
  /** Только админу: веха скрыта. */
  hidden?: boolean;
};

export type BioChapter = { key: string; title: string; from: number; to: number };

export type Written = { from: number; to: number };

/** Работа датасета; work_id — наша works.id, если сопоставилась с каталогом. */
export type BioWork = { ref: string; title: string; qid?: string; written?: Written; work_id?: number };

export type ContextEvent = { key: string; from: number; to?: number; short: string };

export type BioTimeline = {
  available: boolean;
  qid?: string;
  name?: string;
  born?: string;
  died?: string;
  chapters?: BioChapter[];
  events?: BioEvent[];
  works?: BioWork[];
  context?: ContextEvent[];
  sources?: Array<{ id: string; url: string }>;
  version?: string;
};

export type Relation = { kind: 'during' | 'same_year' | 'before'; years?: number };

export type ContextItem = { event: BioEvent; relation: Relation; linked: boolean; note?: string };

export type LifeContext = {
  available: boolean;
  author?: { id: number; name: string };
  period?: Written;
  items?: ContextItem[];
};

export type BioTimelineStats = {
  dataset_version: string;
  seeded_version: string;
  authors_in_dataset: number;
  authors_with_qid: number;
  authors_with_timeline: number;
  hidden_events: number;
  seeded_at: string;
};

export function useBioTimeline(authorId: number | string | undefined) {
  const id = authorId === undefined || authorId === '' ? undefined : String(authorId);
  return useQuery<BioTimeline>({
    queryKey: ['bio-timeline', id],
    queryFn: ({ signal }) => apiFetch<BioTimeline>(`/api/authors/${id}/bio-timeline`, { signal }),
    enabled: id !== undefined,
    staleTime: 5 * 60_000,
  });
}

export function useLifeContext(workId: number | undefined) {
  return useQuery<LifeContext>({
    queryKey: ['life-context', workId],
    queryFn: ({ signal }) => apiFetch<LifeContext>(`/api/works/${workId}/life-context`, { signal }),
    enabled: workId !== undefined && workId > 0,
    staleTime: 5 * 60_000,
  });
}

export function useBioTimelineStats(enabled = true) {
  return useQuery<BioTimelineStats>({
    queryKey: ['admin', 'bio-timeline-stats'],
    queryFn: ({ signal }) => apiFetch<BioTimelineStats>('/api/admin/bio-timeline/stats', { signal }),
    enabled,
  });
}

/** Скрыть/вернуть веху (админ). Инвалидирует таймлайн автора и окна книг. */
export function useSetBioEventHidden() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ qid, key, hidden }: { qid: string; key: string; hidden: boolean }) =>
      apiFetch<{ qid: string; key: string; hidden: boolean }>(
        `/api/admin/bio-timeline/${encodeURIComponent(qid)}/events/${encodeURIComponent(key)}`,
        { method: 'PATCH', body: { hidden } },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['bio-timeline'] });
      void qc.invalidateQueries({ queryKey: ['life-context'] });
    },
  });
}

/* ───────────── даты ───────────── */

const MONTHS_GEN = ['января', 'февраля', 'марта', 'апреля', 'мая', 'июня', 'июля', 'августа', 'сентября', 'октября', 'ноября', 'декабря'];
const MONTHS_NOM = ['январь', 'февраль', 'март', 'апрель', 'май', 'июнь', 'июль', 'август', 'сентябрь', 'октябрь', 'ноябрь', 'декабрь'];

export function yearOf(iso: string): number {
  return Number(iso.slice(0, 4));
}

/** Год окончания вехи: для периода — `to`, иначе `from`. */
export function endYear(e: BioEvent): number {
  return yearOf(e.date.to ?? e.date.from);
}

export function isPeriod(e: BioEvent): boolean {
  return e.date.to != null && yearOf(e.date.to) > yearOf(e.date.from);
}

export function fmtPoint(iso: string, precision: BioDate['precision']): string {
  const y = iso.slice(0, 4);
  if (precision === 'year' || iso.length < 7) return y;
  const m = Number(iso.slice(5, 7)) - 1;
  if (precision === 'month' || iso.length < 10) return `${MONTHS_NOM[m]} ${y}`;
  return `${Number(iso.slice(8, 10))} ${MONTHS_GEN[m]} ${y}`;
}

/** Полная дата для карточки книги: «24 мая 1868», «май 1868», «1850–1854». */
export function fmtDate(d: BioDate): string {
  if (d.to) {
    const a = yearOf(d.from);
    const b = yearOf(d.to);
    return a === b ? `${fmtPoint(d.from, d.precision)} — ${fmtPoint(d.to, d.precision)}` : `${a}–${b}`;
  }
  return fmtPoint(d.from, d.precision);
}

/** Подпись на оси: «1868» или «1850–54». */
export function yearLabel(e: BioEvent): string {
  const a = yearOf(e.date.from);
  const b = endYear(e);
  return a === b ? String(a) : `${a}–${String(b).slice(2)}`;
}

export function fmtPeriod(p: Written): string {
  return p.from === p.to ? String(p.from) : `${p.from}–${p.to}`;
}

export function relationLabel(r: Relation): string {
  switch (r.kind) {
    case 'same_year':
      return 'в год написания';
    case 'during':
      return 'во время работы над книгой';
    case 'before': {
      const n = r.years ?? 0;
      return n === 1 ? 'за год до' : `за ${n} ${pluralRu(n, ['год', 'года', 'лет'])} до`;
    }
  }
}

/** Годы жизни: «1821–1881» или «род. 1922». */
export function lifeYears(born?: string, died?: string): string {
  if (!born) return '';
  return died ? `${yearOf(born)}–${yearOf(died)}` : `род. ${yearOf(born)}`;
}

export function pluralEvents(n: number): string {
  return pluralRu(n, ['веха', 'вехи', 'вех']);
}

/* ───────────── отбор и связи ───────────── */

/** Вехи тизера: major без якорей; если линкованных ≥ 5 — только они; ≤ 7. */
export function teaserEvents(events: BioEvent[], limit = 7): BioEvent[] {
  const majors = events.filter((e) => e.tier === 'major' && e.type !== 'birth' && e.type !== 'death' && !e.hidden);
  const linked = majors.filter((e) => e.works?.length);
  return (linked.length >= 5 ? linked : majors).slice(0, limit);
}

/** Ключ активного элемента подсветки: `e:<event.key>` или `w:<work.ref>`. */
export type ActiveKey = string | null;

/**
 * Что подсвечивать при наведении: сам элемент плюс связанные (веха → её
 * книги, книга → вехи с линком на неё).
 */
export function relatedKeys(events: BioEvent[], active: ActiveKey): Set<string> {
  const out = new Set<string>();
  if (!active) return out;
  out.add(active);
  if (active.startsWith('e:')) {
    const e = events.find((x) => `e:${x.key}` === active);
    e?.works?.forEach((l) => out.add(`w:${l.ref}`));
  } else {
    const ref = active.slice(2);
    events.filter((x) => x.works?.some((l) => l.ref === ref)).forEach((x) => out.add(`e:${x.key}`));
  }
  return out;
}
