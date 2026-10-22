import { queryOptions, useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from './api';

/**
 * recs.ts — лента рекомендаций на Главной (план recsys/01, задача 1.3).
 *
 * Один контракт для всех секций: GET /api/me/recommendations?section=<kind>
 * → { section, items: RecItem[] }, у каждой карточки — причины (RecReason),
 * текст которых собирает фронт (lib/recReasons.ts). Секции на главной
 * запрашиваются по отдельности (свой queryKey, свой staleTime), порядок —
 * в pages/HomePage.tsx. Новая секция = новый RecSectionKind здесь +
 * провайдер на бэке (api/recs.go; секции правил фазы 2 — recsys.Rules).
 */

export type RecSectionKind =
  | 'continue_reading'
  | 'subscriptions'
  | 'rate_prompts'
  | 'searched_appeared'
  | 'next_in_series'
  | 'author_you_finished'
  | 'dropped_then_loved_author'
  | 'forgotten_classic'
  | 'reread'
  | 'fits_tonight'
  | 'to_read_reminder';

/**
 * HOME_SECTIONS — порядок секций на Главной (план recsys/02, 2.9): продолжить →
 * появилось искомое → дальше в цикле → автор, которого дочитали → новинки по
 * подпискам → вы хотели прочитать (recsys/03, 3.3) → брошенное → забытая
 * классика → перечитать → на вечер → оцените прочитанное. Пустые не
 * рендерятся. Зеркало recsys.AllSections.
 */
export const HOME_SECTIONS: readonly RecSectionKind[] = [
  'continue_reading',
  'searched_appeared',
  'next_in_series',
  'author_you_finished',
  'subscriptions',
  'to_read_reminder',
  'dropped_then_loved_author',
  'forgotten_classic',
  'reread',
  'fits_tonight',
  'rate_prompts',
];

/** Чипы «у меня есть N минут» секции «На вечер» (план recsys/02, 2.6) и выбор по умолчанию. */
export const TONIGHT_MINUTES = [20, 40, 60] as const;
export const TONIGHT_DEFAULT_MINUTES = 40;
const TONIGHT_LS_KEY = 'skriptes.tonightMinutes';

/** readTonightMinutes / writeTonightMinutes — выбранный чип (localStorage: удобство браузера, не настройка). */
export function readTonightMinutes(): number {
  if (typeof window === 'undefined') return TONIGHT_DEFAULT_MINUTES;
  try {
    const n = Number(window.localStorage.getItem(TONIGHT_LS_KEY));
    return (TONIGHT_MINUTES as readonly number[]).includes(n) ? n : TONIGHT_DEFAULT_MINUTES;
  } catch {
    return TONIGHT_DEFAULT_MINUTES;
  }
}
export function writeTonightMinutes(n: number) {
  try {
    window.localStorage.setItem(TONIGHT_LS_KEY, String(n));
  } catch {
    /* приватный режим — выбор живёт до перезагрузки */
  }
}

// RecReason — зеркало recsys.Reason (backend): дискриминант kind, у каждого
// вида — свои заполненные поля.
export type RecReason =
  | { kind: 'in_progress'; fraction: number; at?: string; minutes?: number }
  | { kind: 'new_by_author'; author_id: number; author: string; at?: string }
  | { kind: 'new_in_series'; series_id: number; series: string; at?: string }
  | { kind: 'acquired'; at?: string }
  | { kind: 'finished'; at?: string }
  | { kind: 'searched_appeared'; query: string; at?: string }
  // Правила фазы 2: work_id/title — прочитанная работа-основание.
  | { kind: 'next_in_series'; series_id: number; series: string; work_id: number; title: string; ser_no?: number; at?: string }
  | { kind: 'author_you_finished'; author_id: number; author: string; work_id: number; title: string; at?: string }
  // Бросили at, с тех пор полюбили count работ автора.
  | { kind: 'dropped_then_loved_author'; author_id: number; author: string; count: number; at?: string }
  // Оценили на 5, прочитали at, с тех пор не открывали.
  | { kind: 'reread'; at?: string }
  // Классика year года, внешний rating из source; на инстансе никто не открывал.
  | { kind: 'forgotten_classic'; year?: number; rating?: number; source?: 'fantlab' | 'librate' }
  // Укладывается в minutes при вашей скорости; genre — fb2-код любимого жанра.
  | { kind: 'fits_tonight'; minutes?: number; genre?: string }
  // Положили на «Хочу прочитать» at и с тех пор не открывали (recsys/03, 3.3).
  | { kind: 'to_read'; at?: string };

// RecItem — зеркало recsys.Item: id — представительное издание (обложка,
// фолбэк ссылки), work_id — карточка книги. author_ids — те же авторы, что
// authors, в том же порядке; series_id — цикл подписи series (есть только у
// секций правил — kebab «не предлагать», recsys/03 3.2).
export type RecItem = {
  id: number;
  work_id?: number;
  title: string;
  authors: string[];
  author_ids?: number[];
  series?: string;
  series_id?: number;
  lib_id: string;
  cover_path?: string;
  reasons: RecReason[];
};

export type RecSection = { section: RecSectionKind; items: RecItem[] };

export type DismissReason = 'not_interested' | 'already_read' | 'later';

/** Префикс queryKey всех секций ленты (инвалидация и оптимистичное скрытие). */
export const RECS_KEY = ['me', 'recs'] as const;

/** recSectionKey — queryKey секции без лимита (инвалидировать по префиксу). */
export function recSectionKey(kind: RecSectionKind) {
  return [...RECS_KEY, kind] as const;
}

/** queryKey секции «Оцените прочитанное» (мутации в lib/ratings.ts инвалидируют по префиксу). */
export const RATEABLE_FEED_KEY = recSectionKey('rate_prompts');

// staleTime по секциям: прогресс чтения меняется в ридере — хотим свежий
// список при возврате; новинки — только при импорте; запросы оценки —
// инвалидируются мутациями (lib/ratings.ts).
const STALE_MS: Record<RecSectionKind, number> = {
  continue_reading: 10_000,
  subscriptions: 60_000,
  rate_prompts: 30_000,
  searched_appeared: 60_000,
  next_in_series: 60_000,
  author_you_finished: 60_000,
  dropped_then_loved_author: 60_000,
  forgotten_classic: 5 * 60_000,
  reread: 5 * 60_000,
  fits_tonight: 60_000,
  to_read_reminder: 5 * 60_000,
};

/** SectionParams — параметры секции (minutes — «у меня есть N минут», fits_tonight). */
export type SectionParams = { minutes?: number };

/** recSectionQueryOptions — запрос одной секции (для useRecSection и useQueries). */
export function recSectionQueryOptions(kind: RecSectionKind, limit = 12, params: SectionParams = {}) {
  const extra = params.minutes ? `&minutes=${params.minutes}` : '';
  return queryOptions<RecItem[]>({
    queryKey: [...recSectionKey(kind), limit, params.minutes ?? 0],
    queryFn: async ({ signal }) => {
      const r = await apiFetch<RecSection>(
        `/api/me/recommendations?section=${kind}&limit=${limit}${extra}`,
        { signal },
      );
      return r.items;
    },
    staleTime: STALE_MS[kind],
  });
}

/** useRecSection — карточки одной секции ленты. */
export function useRecSection(kind: RecSectionKind, limit = 12) {
  return useQuery(recSectionQueryOptions(kind, limit));
}

/**
 * useHomeSections — все секции Главной разом (порядок HOME_SECTIONS), чтобы
 * страница знала, когда лента пуста целиком (пустое состояние нового
 * пользователя), а не только по секции.
 */
export function useHomeSections(limit = 12, tonightMinutes = TONIGHT_DEFAULT_MINUTES) {
  return useQueries({
    queries: HOME_SECTIONS.map((kind) =>
      recSectionQueryOptions(kind, limit, kind === 'fits_tonight' ? { minutes: tonightMinutes } : {}),
    ),
    combine: (results) => ({
      sections: HOME_SECTIONS.map((kind, i) => ({
        kind,
        items: results[i].data ?? [],
        isLoading: results[i].isLoading,
      })),
      allLoaded: results.every((r) => !r.isLoading),
      allEmpty: results.every((r) => !r.isLoading && (r.data?.length ?? 0) === 0),
    }),
  });
}

/** workIdOf — id работы карточки (фолбэк — издание, если работы нет). */
export function workIdOf(it: Pick<RecItem, 'id' | 'work_id'>): number {
  return it.work_id ?? it.id;
}

/**
 * useDismissRec — скрыть работу из ленты с причиной («не интересно» /
 * «уже читал» / «потом»). Оптимистично убираем работу из ВСЕХ закэшированных
 * секций (скрытие — по работе, она могла лежать в нескольких); на ошибке —
 * откат. Скрытие персистентно (backend feed_dismissals), книга не вернётся.
 */
export function useDismissRec() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ workId, reason }: { workId: number; reason: DismissReason }) => {
      await apiFetch<void>('/api/me/feed/dismiss', {
        method: 'POST',
        body: { work_id: workId, reason },
      });
      return workId;
    },
    onMutate: async ({ workId }) => {
      await qc.cancelQueries({ queryKey: RECS_KEY });
      const prev = qc.getQueriesData<RecItem[]>({ queryKey: RECS_KEY });
      for (const [key, data] of prev) {
        if (data) {
          qc.setQueryData(
            key,
            data.filter((it) => workIdOf(it) !== workId),
          );
        }
      }
      return { prev };
    },
    onError: (_e, _v, ctx) => {
      ctx?.prev?.forEach(([key, data]) => qc.setQueryData(key, data));
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: RECS_KEY });
    },
  });
}

/**
 * useRelatedRecs — «раз понравилось — вот следующая» (план recsys/02, 2.8):
 * до трёх карточек правил, основание которых — работа workId. Запрашивается
 * только когда enabled (после оценки ≥ 4 на карточке).
 */
export function useRelatedRecs(workId: number | undefined, enabled: boolean, limit = 3) {
  return useQuery<RecItem[]>({
    queryKey: [...RECS_KEY, 'related', workId, limit],
    queryFn: async ({ signal }) => {
      const r = await apiFetch<{ work_id: number; items: RecItem[] }>(
        `/api/me/recommendations/related?work_id=${workId}&limit=${limit}`,
        { signal },
      );
      return r.items;
    },
    enabled: enabled && workId != null,
    staleTime: 30_000,
  });
}
