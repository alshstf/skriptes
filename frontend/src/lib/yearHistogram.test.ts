import { describe, expect, it } from 'vitest';
import { binLabel, binStep, binYears, visibleLabels } from './yearHistogram';

describe('visibleLabels', () => {
  it('ставит подписи от самых высоких, налезающие пропускает', () => {
    // Столбики через 4 px: подпись «33» (≈20 px) закрывает соседей.
    const xs = [0, 4, 8, 12, 40];
    const counts = [2, 33, 9, 1, 18];
    expect([...visibleLabels(xs, counts)].sort((a, b) => a - b)).toEqual([1, 4]);
  });
  it('редкие столбики — все подписи', () => {
    expect(visibleLabels([0, 30, 60], [1, 2, 3]).size).toBe(3);
  });
});

describe('binStep / binYears / binLabel', () => {
  const data = [
    { year: 1939, count: 2 },
    { year: 1944, count: 18 },
    { year: 1950, count: 33 },
    { year: 2016, count: 1 },
  ];
  it('шаг — наименьший, при котором столбик не уже 22 px', () => {
    expect(binStep(data, 2000)).toBe(1); // 78 лет × 22 px влезает
    expect(binStep(data, 900)).toBe(2); // 39 корзин × 22 = 858 ≤ 900
    expect(binStep(data, 500)).toBe(5); // 16 × 22 = 352 ≤ 500, а 39 × 22 — нет
    expect(binStep(data, 250)).toBe(10);
    expect(binStep([{ year: 2000, count: 1 }], 100)).toBe(1);
  });
  it('корзины выровнены по кратным шага, пустые — тоже', () => {
    const bins = binYears(data, 10);
    expect(bins.map((b) => [b.year, b.count])).toEqual([
      [1930, 2],
      [1940, 18],
      [1950, 33],
      [1960, 0],
      [1970, 0],
      [1980, 0],
      [1990, 0],
      [2000, 0],
      [2010, 1],
    ]);
    expect(binLabel(bins[0])).toBe('1930-е');
    expect(binLabel({ year: 1945, to: 1949 })).toBe('1945–49');
    expect(binLabel({ year: 1998, to: 2002 })).toBe('1998–2002');
  });
});
