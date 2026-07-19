import { useQuery } from '@tanstack/react-query';
import { apiFetch } from './api';
import type { YearBook } from './catalog';

/**
 * Био-таймлайн автора: события жизни ⟷ книги (план cryptic-roaming-turing).
 * Данные наполняет бэкенд лениво — сам GET и служит триггером, поэтому здесь
 * поллинг до enrichment_status='done' (зеркало bio/photo в useAuthor).
 */

export type AuthorEventType =
  | 'birth'
  | 'death'
  | 'war'
  | 'persecution'
  | 'loss'
  | 'isolation'
  | 'poverty'
  | 'spiritual'
  | 'love'
  | 'child'
  | 'illness'
  | 'relocation'
  | 'career'
  | 'creation_mode'
  | 'education'
  | 'residence'
  | 'award'
  | 'other';

export type AuthorEvent = {
  id: number;
  source: 'wikidata' | 'wikipedia';
  type: AuthorEventType;
  year_from: number;
  /** Период (каторга, эмиграция, брак): год окончания. Нет → точка на оси. */
  year_to?: number;
  date_from?: string;
  date_precision: 'year' | 'month' | 'day';
  title: string;
  /** Сырое предложение источника (wikipedia) — цитата под формулировкой. */
  quote?: string;
  place?: string;
  url?: string;
  weight: number;
  /** Только админу: событие скрыто курированием. */
  hidden?: boolean;
};

export type EventAttribution = { source: string; license: string; url?: string };

export type AuthorEventsResponse = {
  items: AuthorEvent[];
  enrichment_status: 'pending' | 'done';
  /** Критерий «таймлайн не скучен» (бэкенд): false → секцию НЕ рендерим. */
  eligible: boolean;
  attribution?: EventAttribution[];
};

/** Кап поллинга: SPARQL+Wikipedia укладываются в ~30с, дальше не ждём. */
const EVENTS_POLL_MAX_TRIES = 20;

export function useAuthorEvents(id: number | string | undefined) {
  return useQuery<AuthorEventsResponse>({
    queryKey: ['author-events', String(id)],
    queryFn: ({ signal }) =>
      apiFetch<AuthorEventsResponse>(`/api/authors/${id}/events`, { signal }),
    enabled: id !== undefined && id !== '',
    refetchInterval: (q) => {
      if (q.state.data?.enrichment_status === 'done') return false;
      if (q.state.dataUpdateCount > EVENTS_POLL_MAX_TRIES) return false;
      return 3_000;
    },
    staleTime: 60_000,
  });
}

/** Связь события с годом написания книги (считает бэкенд). */
export type EventRelation = 'same_year' | 'during' | 'years_after' | 'delayed';

export type LifeEvent = AuthorEvent & {
  relation: EventRelation;
  /** Сколько лет прошло — для years_after/delayed. */
  years_gap?: number;
};

export type WorkLifeEventsResponse = {
  author_id: number;
  author_name: string;
  written_year?: number;
  items: LifeEvent[];
  eligible: boolean;
  attribution?: EventAttribution[];
};

/**
 * useWorkLifeEvents — блок «В жизни автора в это время» на карточке книги.
 * Эндпоинт сам триггерит обогащение автора, поэтому поллим, пока блок не
 * станет eligible (данные общие с таймлайном автора).
 */
export function useWorkLifeEvents(workId: number | undefined) {
  return useQuery<WorkLifeEventsResponse>({
    queryKey: ['work-life-events', String(workId)],
    queryFn: ({ signal }) =>
      apiFetch<WorkLifeEventsResponse>(`/api/works/${workId}/life-events`, { signal }),
    enabled: !!workId,
    refetchInterval: (q) => {
      const d = q.state.data;
      // Нет автора или года написания — ждать нечего, связей не будет.
      if (!d || !d.author_id || !d.written_year) return false;
      if (d.eligible) return false;
      if (q.state.dataUpdateCount > EVENTS_POLL_MAX_TRIES) return false;
      return 3_000;
    },
    staleTime: 60_000,
  });
}

/**
 * Год написания книги → сами книги (правая сторона таймлайна). Совместим с
 * catalog.YearCount: books там опционален (в гистограмме нужен только count).
 */
export type TimelineBooks = { year: number; books?: YearBook[] };

/** Строка таймлайна: год (или период), его события и книги этого года. */
export type TimelineRow = {
  year: number;
  events: AuthorEvent[];
  books: YearBook[];
};

/**
 * buildTimeline — слияние событий и книг в общую ось лет.
 *
 * Периоды (year_to) на оси живут ОДНОЙ строкой своего начала — растягивать их
 * на все годы значило бы дублировать «каторга» пять раз; накрытые годы UI
 * показывает лентой вдоль оси (spansAt).
 */
export function buildTimeline(events: AuthorEvent[], yearStats: TimelineBooks[]): TimelineRow[] {
  const byYear = new Map<number, TimelineRow>();
  const row = (year: number): TimelineRow => {
    let r = byYear.get(year);
    if (!r) {
      r = { year, events: [], books: [] };
      byYear.set(year, r);
    }
    return r;
  };
  for (const ev of events) row(ev.year_from).events.push(ev);
  for (const ys of yearStats) {
    if (!ys.books?.length) continue;
    row(ys.year).books.push(...ys.books);
  }
  const rows = [...byYear.values()].sort((a, b) => a.year - b.year);
  // Внутри года: сначала весомые события (утрата важнее мелкой награды).
  for (const r of rows) r.events.sort((a, b) => b.weight - a.weight);
  return rows;
}

/** Периоды, накрывающие год (каторга 1850–54 «проходит» через 1852). */
export function spansAt(events: AuthorEvent[], year: number): AuthorEvent[] {
  return events.filter(
    (ev) => ev.year_to != null && ev.year_from < year && ev.year_to >= year,
  );
}

/**
 * Разрыв в оси: сколько пустых лет между соседними строками. UI схлопывает
 * их в «· · · N лет» — иначе у автора с 40-летним молчанием таймлайн
 * растянется пустотой.
 */
export const TIMELINE_GAP_MIN = 4;

/**
 * collapseRows — что показать в свёрнутом виде.
 *
 * Наивный slice(0, N) обрезал таймлайн по первым годам жизни — у Толстого
 * свёрнутая секция не показывала НИ ОДНОЙ книги (они написаны в 1860-70-х),
 * то есть теряла ровно тот смысл, ради которого существует. Поэтому строки с
 * книгами приоритетны: сначала они, затем их «окружение» (годы окна связи),
 * затем самые весомые события; результат — снова по возрастанию года.
 */
export function collapseRows(rows: TimelineRow[], limit: number): TimelineRow[] {
  if (rows.length <= limit) return rows;

  const picked = new Set<number>();
  const take = (year: number) => {
    if (picked.size < limit) picked.add(year);
  };

  const withBooks = rows.filter((r) => r.books.length > 0);
  for (const r of withBooks) take(r.year);
  // Окружение книг: годы, которые UI подсветит при наведении.
  for (const r of withBooks) {
    for (const y of relatedYears(r.year)) {
      if (rows.some((row) => row.year === y)) take(y);
    }
  }
  // Добор самыми весомыми событиями (война/утрата/травля вперёд).
  const byWeight = [...rows].sort(
    (a, b) => maxWeight(b) - maxWeight(a) || a.year - b.year,
  );
  for (const r of byWeight) take(r.year);

  return rows.filter((r) => picked.has(r.year));
}

function maxWeight(row: TimelineRow): number {
  return row.events.reduce((m, e) => Math.max(m, e.weight), 0);
}

/**
 * relatedYears — какие годы подсвечивать при наведении на книгу года Y.
 * ТОЛЬКО годовая арифметика: written_year — год без месяца, точнее нельзя
 * (грабля №21). Окно [Y-2..Y] — «что происходило, когда писалась книга».
 */
export function relatedYears(year: number): number[] {
  return [year - 2, year - 1, year];
}

/**
 * relationPhrase — человеческая формулировка связи. ТОЛЬКО годовая точность
 * (грабля №21): «в год», «за N лет до» — и никаких месяцев, даже если у
 * события известен день.
 */
export function relationPhrase(it: LifeEvent): string {
  switch (it.relation) {
    case 'same_year':
      return 'в тот же год';
    case 'during':
      return it.year_to ? `тянулось с ${it.year_from} по ${it.year_to}` : 'в это время';
    case 'years_after':
      return `за ${pluralYears(it.years_gap ?? 0)} до`;
    case 'delayed':
      return `за ${pluralYears(it.years_gap ?? 0)} до этого`;
    default:
      return String(it.year_from);
  }
}

/** Русская плюрализация лет: 1 год / 3 года / 5 лет / 11 лет / 21 год. */
export function pluralYears(years: number): string {
  const n = Math.abs(years) % 100;
  const n1 = n % 10;
  if (n > 10 && n < 20) return `${years} лет`;
  if (n1 === 1) return `${years} год`;
  if (n1 >= 2 && n1 <= 4) return `${years} года`;
  return `${years} лет`;
}
