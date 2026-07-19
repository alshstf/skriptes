import { describe, it, expect } from 'vitest';
import {
  buildTimeline,
  relatedYears,
  spansAt,
  TIMELINE_GAP_MIN,
  pluralYears,
  type AuthorEvent,
} from './authorEvents';

const ev = (p: Partial<AuthorEvent> & { id: number; year_from: number }): AuthorEvent => ({
  source: 'wikidata',
  type: 'other',
  date_precision: 'year',
  title: 'Событие',
  weight: 1,
  ...p,
});

describe('buildTimeline', () => {
  it('сливает события и книги в одну ось лет по возрастанию', () => {
    const rows = buildTimeline(
      [ev({ id: 1, year_from: 1866, type: 'love', title: 'Брак' }), ev({ id: 2, year_from: 1849 })],
      [
        { year: 1866, books: [{ id: 10, title: 'Преступление и наказание' }] },
        { year: 1869, books: [{ id: 11, title: 'Идиот' }] },
      ],
    );
    expect(rows.map((r) => r.year)).toEqual([1849, 1866, 1869]);
    // Год, где сошлись событие и книга, — одна строка (в этом вся суть секции).
    const y1866 = rows.find((r) => r.year === 1866)!;
    expect(y1866.events).toHaveLength(1);
    expect(y1866.books).toHaveLength(1);
  });

  it('внутри года весомые события идут первыми', () => {
    const rows = buildTimeline(
      [
        ev({ id: 1, year_from: 1868, type: 'award', title: 'Премия', weight: 1 }),
        ev({ id: 2, year_from: 1868, type: 'loss', title: 'Смерть дочери', weight: 5 }),
      ],
      [],
    );
    expect(rows[0].events.map((e) => e.title)).toEqual(['Смерть дочери', 'Премия']);
  });

  it('годы без книг не создают строк (year_stats с пустым books)', () => {
    const rows = buildTimeline([], [{ year: 1900, books: [] }]);
    expect(rows).toHaveLength(0);
  });
});

describe('spansAt', () => {
  const katorga = ev({ id: 1, year_from: 1850, year_to: 1854, type: 'persecution' });

  it('период накрывает промежуточные годы, но не свой первый (он — своя строка)', () => {
    expect(spansAt([katorga], 1852)).toHaveLength(1);
    expect(spansAt([katorga], 1854)).toHaveLength(1);
    expect(spansAt([katorga], 1850)).toHaveLength(0);
    expect(spansAt([katorga], 1855)).toHaveLength(0);
  });

  it('точечные события периодами не считаются', () => {
    expect(spansAt([ev({ id: 2, year_from: 1852 })], 1852)).toHaveLength(0);
  });
});

describe('relatedYears', () => {
  it('подсвечивает окно [Y-2..Y] — только годовая арифметика (грабля №21)', () => {
    expect(relatedYears(1869)).toEqual([1867, 1868, 1869]);
  });
});

describe('TIMELINE_GAP_MIN', () => {
  it('разрыв схлопывается от 4 лет — соседние годы остаются рядом', () => {
    expect(TIMELINE_GAP_MIN).toBe(4);
  });
});

describe('pluralYears', () => {
  it('склоняет годы по-русски (в разрывах оси было «3 лет»)', () => {
    expect(pluralYears(1)).toBe('1 год');
    expect(pluralYears(3)).toBe('3 года');
    expect(pluralYears(5)).toBe('5 лет');
    expect(pluralYears(13)).toBe('13 лет');
    expect(pluralYears(21)).toBe('21 год');
  });
});
