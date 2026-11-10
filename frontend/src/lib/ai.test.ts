import { describe, expect, it } from 'vitest';
import { formatUsd, parseBudget } from './ai';

describe('parseBudget', () => {
  it('принимает число с точкой или запятой, не больше двух знаков после', () => {
    expect(parseBudget('10', 1000)).toBe(10);
    expect(parseBudget(' 3,5 ', 1000)).toBe(3.5);
    expect(parseBudget('0', 1000)).toBe(0);
    expect(parseBudget('7.25', 1000)).toBe(7.25);
  });
  it('отклоняет мусор, отрицательное и выше предела', () => {
    for (const bad of ['', 'abc', '-1', '1.234', '1e3', '10 $']) expect(parseBudget(bad, 1000)).toBeNull();
    expect(parseBudget('1001', 1000)).toBeNull();
  });
});

describe('formatUsd', () => {
  it('два знака и запятая', () => {
    expect(formatUsd(0.4)).toBe('0,40 $');
    expect(formatUsd(10)).toBe('10,00 $');
  });
});
