import { useQuery } from '@tanstack/react-query';
import { apiFetch } from './api';
import type { BookListItem } from './books';

// Премии (#389): лауреаты премий из белого списка владельца — Фантлаб, Wikidata,
// список в репозитории (русская Википедия). Книга или автор из каталога — ссылкой,
// остальные — строкой. Кинопремии (film) — только экранизации книг каталога.

export type Award = {
  key: string;
  name: string;
  group: string;
  author_level?: boolean;
  max_year?: number;
  film?: boolean;
};

export type AwardSummary = Award & {
  wins: number;
  in_catalog: number;
  first_year?: number;
  last_year?: number;
};

export type AwardWin = {
  id: number;
  year: number;
  nomination?: string;
  kind: 'work' | 'author';
  title?: string;
  orig_title?: string;
  author: string;
  work_id?: number;
  author_id?: number;
  source: 'fantlab' | 'wikidata' | 'manual';
  source_url?: string;
  // Карточка книги — если она в каталоге и не скрыта настройками контента.
  item?: BookListItem;
};

export type AwardBadge = {
  key: string;
  name: string;
  year: number;
  nomination?: string;
  // film — название фильма или сериала: премия экранизации книги.
  film?: string;
};

/** sourceLabel — подпись ссылки на источник строки лауреата. */
export function sourceLabel(source: AwardWin['source']): string {
  return source === 'wikidata' ? 'Wikidata' : source === 'manual' ? 'Википедия' : 'Фантлаб';
}

const STALE = 5 * 60_000;

/** useAwards — премии раздела со счётчиками лауреатов. */
export function useAwards() {
  return useQuery<AwardSummary[]>({
    queryKey: ['awards'],
    queryFn: async () => (await apiFetch<{ items: AwardSummary[] }>('/api/awards')).items,
    staleTime: STALE,
  });
}

/** useAwardWins — лауреаты премии (свежие годы сверху). */
export function useAwardWins(key: string) {
  return useQuery<{ award: Award; wins: AwardWin[] }>({
    queryKey: ['awards', key],
    queryFn: () => apiFetch(`/api/awards/${encodeURIComponent(key)}`),
    staleTime: STALE,
  });
}

/** useWorkAwards — премии книги (плашки на карточке). */
export function useWorkAwards(workId: number | undefined) {
  return useQuery<AwardBadge[]>({
    queryKey: ['awards', 'work', workId ?? 0],
    queryFn: async () => (await apiFetch<{ items: AwardBadge[] }>(`/api/works/${workId}/awards`)).items,
    enabled: workId != null && workId > 0,
    staleTime: STALE,
  });
}

/** useAuthorAwards — премии, врученные автору (Нобелевская, «Аэлита»). */
export function useAuthorAwards(authorId: number | undefined) {
  return useQuery<AwardBadge[]>({
    queryKey: ['awards', 'author', authorId ?? 0],
    queryFn: async () => (await apiFetch<{ items: AwardBadge[] }>(`/api/authors/${authorId}/awards`)).items,
    enabled: authorId != null && authorId > 0,
    staleTime: STALE,
  });
}

/** yearAnchor — якорь года на странице премии (#y1966). */
export function yearAnchor(year: number): string {
  return `y${year}`;
}

/** groupByYear — лауреаты по годам в порядке ответа (свежие сверху), внутри года — по номинациям. */
export function groupByYear(wins: AwardWin[]): { year: number; noms: { nomination: string; wins: AwardWin[] }[] }[] {
  const out: { year: number; noms: { nomination: string; wins: AwardWin[] }[] }[] = [];
  for (const w of wins) {
    let y = out[out.length - 1];
    if (!y || y.year !== w.year) {
      y = { year: w.year, noms: [] };
      out.push(y);
    }
    const nom = w.nomination ?? '';
    let n = y.noms.find((x) => x.nomination === nom);
    if (!n) {
      n = { nomination: nom, wins: [] };
      y.noms.push(n);
    }
    n.wins.push(w);
  }
  return out;
}

/**
 * mergeBadges — одна плашка на премию и год: «Оскар 1940» за лучший фильм и за
 * сценарий — одна плашка, номинации (и фильмы) — через запятую в подсказке.
 */
export function mergeBadges(badges: AwardBadge[]): AwardBadge[] {
  const out: AwardBadge[] = [];
  for (const b of badges) {
    const same = out.find((x) => x.key === b.key && x.year === b.year);
    if (!same) {
      out.push({ ...b });
      continue;
    }
    if (b.nomination && !(same.nomination ?? '').split(', ').includes(b.nomination)) {
      same.nomination = same.nomination ? `${same.nomination}, ${b.nomination}` : b.nomination;
    }
    if (b.film && !(same.film ?? '').split('», «').includes(b.film)) {
      same.film = same.film ? `${same.film}», «${b.film}` : b.film;
    }
  }
  return out;
}
