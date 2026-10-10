/**
 * lib/readerSettings.ts — настройки веб-ридера: тема страницы, размер шрифта,
 * поля, способы перелистывания.
 *
 * Хранение — только localStorage этого устройства, без сервера: на телефоне и
 * на компьютере нужны разные шрифт, поля и способы листать (тап-зоны —
 * телефонная привычка). Ридер (iframe /foliate-reader.html) получает готовые
 * значения — цвета, пиксели, флаги (`ReaderFrameSettings`): стартовые в URL,
 * изменения — postMessage `{type:'settings'}`. Тема «Авто» решается здесь, по
 * системной теме устройства (приложение само всегда тёмное).
 */

import { useCallback, useEffect, useRef, useState } from 'react';

export type ReaderTheme = 'auto' | 'light' | 'sepia' | 'dark' | 'black';
export type ReaderMargins = 'narrow' | 'normal' | 'wide';

export type ReaderSettings = {
  theme: ReaderTheme;
  /** Размер шрифта книги, % от исходного. */
  fontSize: number;
  margins: ReaderMargins;
  /** Перелистывание свайпом: страница едет за пальцем. */
  swipe: boolean;
  /** Перелистывание тапом: левый край — назад, правый — вперёд. */
  tapZones: boolean;
  /** Анимация перелистывания (иначе страница меняется мгновенно). */
  animation: boolean;
};

export const READER_DEFAULTS: ReaderSettings = {
  theme: 'dark',
  fontSize: 100,
  margins: 'narrow',
  swipe: true,
  tapZones: true,
  animation: true,
};

export const FONT_SIZE_MIN = 70;
export const FONT_SIZE_MAX = 200;
export const FONT_SIZE_STEP = 10;

type ThemeColors = { label: string; bg: string; fg: string; dark: boolean };

export const READER_THEMES: Record<Exclude<ReaderTheme, 'auto'>, ThemeColors> = {
  light: { label: 'Светлая', bg: '#ffffff', fg: '#1a1a1a', dark: false },
  sepia: { label: 'Сепия', bg: '#f4ecd8', fg: '#5b4636', dark: false },
  dark: { label: 'Тёмная', bg: '#141414', fg: '#d0d0d0', dark: true },
  black: { label: 'Чёрная', bg: '#000000', fg: '#b0b0b0', dark: true },
};

/**
 * Поля страницы: margin — сверху и снизу (px, там же колонтитулы: глава и
 * процент), gap — промежуток колонок в % ширины (по краям — половина).
 */
export const READER_MARGINS: Record<ReaderMargins, { label: string; margin: number; gap: number }> = {
  narrow: { label: 'Узкие', margin: 24, gap: 4 },
  normal: { label: 'Средние', margin: 40, gap: 7 },
  wide: { label: 'Широкие', margin: 56, gap: 11 },
};

/** То, что получает iframe ридера, — без «авто» и названий. */
export type ReaderFrameSettings = {
  bg: string;
  fg: string;
  dark: boolean;
  fontSize: number;
  margin: number;
  gap: number;
  swipe: boolean;
  tap: boolean;
  animated: boolean;
};

export function resolveTheme(theme: ReaderTheme, systemDark: boolean): ThemeColors {
  if (theme === 'auto') return systemDark ? READER_THEMES.dark : READER_THEMES.light;
  return READER_THEMES[theme];
}

export function frameSettings(s: ReaderSettings, systemDark: boolean): ReaderFrameSettings {
  const { bg, fg, dark } = resolveTheme(s.theme, systemDark);
  const { margin, gap } = READER_MARGINS[s.margins];
  return {
    bg,
    fg,
    dark,
    fontSize: s.fontSize,
    margin,
    gap,
    swipe: s.swipe,
    tap: s.tapZones,
    animated: s.animation,
  };
}

const LS_KEY = 'skriptes.reader';
// Live-обновление в этой вкладке (storage-событие приходит только в другие).
const EVT = 'skriptes:reader-settings-changed';

const THEMES: ReaderTheme[] = ['auto', 'light', 'sepia', 'dark', 'black'];
const MARGINS: ReaderMargins[] = ['narrow', 'normal', 'wide'];

export function normalizeReaderSettings(raw: unknown): ReaderSettings {
  const r = (raw && typeof raw === 'object' ? raw : {}) as Partial<Record<keyof ReaderSettings, unknown>>;
  const d = READER_DEFAULTS;
  const bool = (v: unknown, def: boolean) => (typeof v === 'boolean' ? v : def);
  const size =
    typeof r.fontSize === 'number' && Number.isFinite(r.fontSize)
      ? Math.round(Math.min(FONT_SIZE_MAX, Math.max(FONT_SIZE_MIN, r.fontSize)))
      : d.fontSize;
  const s: ReaderSettings = {
    theme: THEMES.includes(r.theme as ReaderTheme) ? (r.theme as ReaderTheme) : d.theme,
    fontSize: size,
    margins: MARGINS.includes(r.margins as ReaderMargins) ? (r.margins as ReaderMargins) : d.margins,
    swipe: bool(r.swipe, d.swipe),
    tapZones: bool(r.tapZones, d.tapZones),
    animation: bool(r.animation, d.animation),
  };
  // Без обоих способов страницу на телефоне не перелистнуть — тапы остаются.
  if (!s.swipe && !s.tapZones) s.tapZones = true;
  return s;
}

export function readReaderSettings(): ReaderSettings {
  if (typeof window === 'undefined') return READER_DEFAULTS;
  try {
    const raw = window.localStorage.getItem(LS_KEY);
    return raw ? normalizeReaderSettings(JSON.parse(raw)) : READER_DEFAULTS;
  } catch {
    return READER_DEFAULTS;
  }
}

function writeReaderSettings(s: ReaderSettings) {
  try {
    window.localStorage.setItem(LS_KEY, JSON.stringify(s));
  } catch {
    /* private mode / quota — настройка живёт до перезагрузки */
  }
  window.dispatchEvent(new CustomEvent(EVT, { detail: s }));
}

/** useReaderSettings — текущие настройки и изменение частями (сразу в localStorage). */
export function useReaderSettings(): [ReaderSettings, (patch: Partial<ReaderSettings>) => void] {
  const [settings, setSettings] = useState<ReaderSettings>(readReaderSettings);
  useEffect(() => {
    const sync = (e: Event) =>
      setSettings(e instanceof CustomEvent && e.detail ? (e.detail as ReaderSettings) : readReaderSettings());
    window.addEventListener(EVT, sync);
    window.addEventListener('storage', sync);
    return () => {
      window.removeEventListener(EVT, sync);
      window.removeEventListener('storage', sync);
    };
  }, []);
  const latest = useRef(settings);
  latest.current = settings;
  const update = useCallback((patch: Partial<ReaderSettings>) => {
    const next = normalizeReaderSettings({ ...latest.current, ...patch });
    latest.current = next;
    setSettings(next);
    writeReaderSettings(next);
  }, []);
  return [settings, update];
}

/** useSystemDark — системная тема устройства (для темы ридера «Авто»). */
export function useSystemDark(): boolean {
  const query = '(prefers-color-scheme: dark)';
  const [dark, setDark] = useState(() => typeof window !== 'undefined' && window.matchMedia?.(query).matches);
  useEffect(() => {
    const mq = window.matchMedia?.(query);
    if (!mq) return;
    const onChange = () => setDark(mq.matches);
    mq.addEventListener('change', onChange);
    return () => mq.removeEventListener('change', onChange);
  }, []);
  return Boolean(dark);
}
