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
  | 'to_read_reminder'
  | 'mood';

/**
 * HOME_SECTIONS — порядок секций на Главной (план recsys/02, 2.9): продолжить →
 * появилось искомое → дальше в цикле → автор, которого дочитали → новинки по
 * подпискам → вы хотели прочитать (recsys/03, 3.3) → брошенное → забытая
 * классика → перечитать → на вечер → оцените прочитанное. Пустые не
 * рендерятся. Первой — «Под настроение» (#580): только при выбранном чипе,
 * чтобы его эффект был виден сразу. Зеркало recsys.AllSections.
 */
export const HOME_SECTIONS: readonly RecSectionKind[] = [
  'mood',
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

/**
 * Настроение сессии (план recsys/03, 3.4): чипы над лентой «лёгкое · умное ·
 * мрачное · смешное · короткое». Выбор живёт в localStorage на вечер
 * (MOOD_TTL_MS) и уходит параметром &mood= в секции MOOD_SECTIONS; каждый
 * выбор логируется (POST /api/me/mood). Зеркало recsys.AllMoods.
 */
export type Mood = 'light' | 'smart' | 'dark' | 'funny' | 'short';
export const MOODS: readonly { key: Mood; label: string }[] = [
  { key: 'light', label: 'лёгкое' },
  { key: 'smart', label: 'умное' },
  { key: 'dark', label: 'мрачное' },
  { key: 'funny', label: 'смешное' },
  { key: 'short', label: 'короткое' },
];
export function isMood(s: unknown): s is Mood {
  return MOODS.some((m) => m.key === s);
}
export function moodLabel(m: Mood): string {
  return MOODS.find((x) => x.key === m)?.label ?? m;
}
/** MOOD_TTL_MS — настроение живёт вечер: через 12 часов чип сбрасывается сам. */
export const MOOD_TTL_MS = 12 * 3_600_000;
const MOOD_LS_KEY = 'skriptes.mood';

/** readMood — выбранный чип, если он не старше MOOD_TTL_MS; иначе ничего. */
export function readMood(now = Date.now()): Mood | undefined {
  if (typeof window === 'undefined') return undefined;
  try {
    const raw = window.localStorage.getItem(MOOD_LS_KEY);
    if (!raw) return undefined;
    const v = JSON.parse(raw) as { mood?: unknown; at?: unknown };
    if (!isMood(v.mood) || typeof v.at !== 'number' || now - v.at > MOOD_TTL_MS) return undefined;
    return v.mood;
  } catch {
    return undefined;
  }
}
/** writeMood — запомнить чип (undefined — снять). */
export function writeMood(m: Mood | undefined, now = Date.now()) {
  try {
    if (m) window.localStorage.setItem(MOOD_LS_KEY, JSON.stringify({ mood: m, at: now }));
    else window.localStorage.removeItem(MOOD_LS_KEY);
  } catch {
    /* приватный режим — выбор живёт до перезагрузки */
  }
}

/**
 * MOOD_SECTIONS — секции, которые учитывают настроение: «Под настроение»
 * (#580: известное в каталоге под настроение, без него не запрашивается),
 * правила фазы 2 (пересортировка, «Забытая классика» — фильтр) и «На вечер»
 * (пересечение). Остальные при смене чипа не перезапрашиваются.
 */
export const MOOD_SECTIONS: ReadonlySet<RecSectionKind> = new Set<RecSectionKind>([
  'mood',
  'next_in_series',
  'author_you_finished',
  'to_read_reminder',
  'dropped_then_loved_author',
  'forgotten_classic',
  'reread',
  'fits_tonight',
]);

// RecReason — зеркало recsys.Reason (backend): дискриминант kind, у каждого
// вида — свои заполненные поля. mood — карточка поднята под настроение
// (recsys/03, 3.4), у любого вида.
export type RecReason = RecReasonBase & { mood?: Mood };
type RecReasonBase =
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
  | { kind: 'to_read'; at?: string }
  // Под настроение (#580): genre — fb2-код жанра настроения, minutes — «≈ N минут» у «короткого».
  | { kind: 'mood'; genre?: string; minutes?: number };

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
  mood: 5 * 60_000,
};

/** SectionParams — параметры секции: minutes — «у меня есть N минут» (fits_tonight), mood — настроение (MOOD_SECTIONS). */
export type SectionParams = { minutes?: number; mood?: Mood };

/** recSectionQueryOptions — запрос одной секции (для useRecSection и useQueries). */
export function recSectionQueryOptions(kind: RecSectionKind, limit = 12, params: SectionParams = {}) {
  const extra = (params.minutes ? `&minutes=${params.minutes}` : '') + (params.mood ? `&mood=${params.mood}` : '');
  return queryOptions<RecItem[]>({
    queryKey: [...recSectionKey(kind), limit, params.minutes ?? 0, params.mood ?? ''],
    // «Под настроение» без выбранного чипа не спрашиваем — секции нет.
    enabled: kind !== 'mood' || params.mood != null,
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
export function useHomeSections(limit = 12, tonightMinutes = TONIGHT_DEFAULT_MINUTES, mood?: Mood) {
  return useQueries({
    queries: HOME_SECTIONS.map((kind) =>
      recSectionQueryOptions(kind, limit, {
        ...(kind === 'fits_tonight' ? { minutes: tonightMinutes } : {}),
        ...(mood && MOOD_SECTIONS.has(kind) ? { mood } : {}),
      }),
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

/** useLogMood — записать выбор чипа настроения (POST /api/me/mood); лента об этом не знает — ей хватает &mood=. */
export function useLogMood() {
  return useMutation({
    mutationFn: async (mood: Mood) => {
      await apiFetch<{ mood: Mood }>('/api/me/mood', { method: 'POST', body: { mood } });
    },
  });
}
