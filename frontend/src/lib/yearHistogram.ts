import { useState } from 'react';
import type { YearCount } from '@/lib/catalog';

/**
 * Режимы гистограммы «по годам написания» (#530). У плодовитых авторов
 * (Брэдбери: 64 года с книгами на 78 лет — ~3,5 px на год на телефоне) подписи
 * над столбиками налезали друг на друга. Владелец выбрал оба режима с
 * переключателем:
 *  - 'years' — столбик на год, подписи от самых высоких столбиков, налезающая
 *    на уже поставленную пропускается (пики подписаны, точное — в тултипе);
 *  - 'periods' — годы в корзины по 2/5/10… лет, чтобы столбик был не уже
 *    BIN_MIN_PX; подписи у всех столбиков.
 * Выбор — на браузер (localStorage), по умолчанию 'years'.
 */
export type HistogramMode = 'years' | 'periods';

export const LABEL_PX_PER_DIGIT = 7; // fontSize 11, tabular-цифры
const LABEL_GAP = 3;
export const BIN_MIN_PX = 22;
const BIN_STEPS = [1, 2, 5, 10, 20, 25, 50];

/** YearBin — корзина лет [year, to] с суммой книг и их списком (для тултипа). */
export type YearBin = YearCount & { to: number };

/** visibleLabels — индексы столбиков, чьи подписи (центр — xs[i]) не налезают
 *  друг на друга: сначала самые высокие, при равных — левее. */
export function visibleLabels(xs: number[], counts: number[]): Set<number> {
  const order = counts.map((_, i) => i).sort((a, b) => counts[b] - counts[a] || a - b);
  const placed: Array<[number, number]> = [];
  const out = new Set<number>();
  for (const i of order) {
    const half = (String(counts[i]).length * LABEL_PX_PER_DIGIT) / 2 + LABEL_GAP;
    const lo = xs[i] - half;
    const hi = xs[i] + half;
    if (placed.some(([a, b]) => lo < b && hi > a)) continue;
    placed.push([lo, hi]);
    out.add(i);
  }
  return out;
}

/** binStep — наименьший шаг корзины (лет), при котором столбик не уже
 *  BIN_MIN_PX; 1 — корзины не нужны (всё и так влезает). data — по возрастанию года. */
export function binStep(data: YearCount[], plotWidth: number): number {
  if (data.length < 2 || plotWidth <= 0) return 1;
  const span = data[data.length - 1].year - data[0].year + 1;
  for (const s of BIN_STEPS) {
    if (Math.ceil(span / s) * BIN_MIN_PX <= plotWidth) return s;
  }
  return BIN_STEPS[BIN_STEPS.length - 1];
}

/** binYears — корзины по step лет, выровненные по кратным step, от первой до
 *  последней (пустые тоже — ось времени остаётся пропорциональной). */
export function binYears(data: YearCount[], step: number): YearBin[] {
  if (data.length === 0) return [];
  const start = Math.floor(data[0].year / step) * step;
  const end = data[data.length - 1].year;
  const bins: YearBin[] = [];
  for (let y = start; y <= end; y += step) bins.push({ year: y, to: y + step - 1, count: 0, books: [] });
  for (const d of data) {
    const b = bins[Math.floor((d.year - start) / step)];
    b.count += d.count;
    b.books = [...(b.books ?? []), ...(d.books ?? [])];
  }
  return bins;
}

/** binLabel — подпись корзины: «1940-е» для десятилетия, иначе «1945–49». */
export function binLabel(b: Pick<YearBin, 'year' | 'to'>): string {
  if (b.to - b.year === 9 && b.year % 10 === 0) return `${b.year}-е`;
  const to = String(b.to);
  return `${b.year}–${String(b.year).slice(0, 2) === to.slice(0, 2) ? to.slice(2) : to}`;
}

const LS_KEY = 'skriptes:histogram-mode';

function readMode(): HistogramMode {
  try {
    return window.localStorage.getItem(LS_KEY) === 'periods' ? 'periods' : 'years';
  } catch {
    return 'years';
  }
}

/** useHistogramMode — выбранный режим гистограмм (общий для автора и серии). */
export function useHistogramMode(): [HistogramMode, (m: HistogramMode) => void] {
  const [mode, setMode] = useState<HistogramMode>(readMode);
  const set = (m: HistogramMode) => {
    setMode(m);
    try {
      window.localStorage.setItem(LS_KEY, m);
    } catch {
      // приватный режим / заблокированное хранилище — выбор живёт до перезагрузки
    }
  };
  return [mode, set];
}
