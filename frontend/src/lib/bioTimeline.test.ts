import { describe, expect, it } from 'vitest';
import { fmtDate, lifeYears, relatedKeys, relationLabel, teaserEvents, yearLabel, type BioEvent } from './bioTimeline';

const ev = (key: string, from: string, tier: 'major' | 'minor', extra: Partial<BioEvent> = {}): BioEvent => ({
  key,
  date: { from, precision: from.length === 4 ? 'year' : from.length === 7 ? 'month' : 'day' },
  type: (key.split('-')[0] as BioEvent['type']) ?? 'other',
  tier,
  short: key,
  ...extra,
});

describe('bioTimeline helpers', () => {
  it('fmtDate: точность и периоды', () => {
    expect(fmtDate({ from: '1868-05-24', precision: 'day' })).toBe('24 мая 1868');
    expect(fmtDate({ from: '1868-05', precision: 'month' })).toBe('май 1868');
    expect(fmtDate({ from: '1868', precision: 'year' })).toBe('1868');
    expect(fmtDate({ from: '1850-02-04', to: '1854-02-27', precision: 'day' })).toBe('1850–1854');
    expect(fmtDate({ from: '1866-10-16', to: '1866-11-10', precision: 'day' })).toBe('16 октября 1866 — 10 ноября 1866');
  });

  it('yearLabel: год или 1850–54', () => {
    expect(yearLabel(ev('loss-1868', '1868-05-24', 'major'))).toBe('1868');
    expect(yearLabel(ev('persecution-1850', '1850-02-04', 'major', { date: { from: '1850-02-04', to: '1854-02-27', precision: 'day' } }))).toBe('1850–54');
  });

  it('relationLabel: склонение «за N лет до»', () => {
    expect(relationLabel({ kind: 'same_year' })).toBe('в год написания');
    expect(relationLabel({ kind: 'during' })).toBe('во время работы над книгой');
    expect(relationLabel({ kind: 'before', years: 1 })).toBe('за год до');
    expect(relationLabel({ kind: 'before', years: 3 })).toBe('за 3 года до');
    expect(relationLabel({ kind: 'before', years: 24 })).toBe('за 24 года до');
    expect(relationLabel({ kind: 'before', years: 12 })).toBe('за 12 лет до');
  });

  it('lifeYears', () => {
    expect(lifeYears('1821-11-11', '1881-02-09')).toBe('1821–1881');
    expect(lifeYears('1922-11-11')).toBe('род. 1922');
    expect(lifeYears(undefined)).toBe('');
  });

  it('teaserEvents: major без якорей и скрытых; линкованные — если их ≥ 5; кап 7', () => {
    const events = [
      ev('birth-1821', '1821', 'major'),
      ev('loss-1837', '1837', 'major'),
      ev('career-1861', '1861', 'minor'),
      ev('loss-1868', '1868', 'major', { works: [{ ref: 'idiot' }], hidden: true }),
      ev('love-1867', '1867', 'major'),
      ev('death-1881', '1881', 'major'),
    ];
    expect(teaserEvents(events).map((e) => e.key)).toEqual(['loss-1837', 'love-1867']);
    const linked = Array.from({ length: 6 }, (_, i) => ev(`career-${1850 + i}`, String(1850 + i), 'major', { works: [{ ref: 'w' }] }));
    const all = [...linked, ev('loss-1870', '1870', 'major'), ev('loss-1871', '1871', 'major')];
    expect(teaserEvents(all).map((e) => e.key)).toEqual(linked.map((e) => e.key));
    expect(teaserEvents(all, 3)).toHaveLength(3);
  });

  it('relatedKeys: веха → её книги, книга → её вехи', () => {
    const events = [ev('loss-1868', '1868', 'major', { works: [{ ref: 'idiot' }] }), ev('love-1867', '1867', 'major')];
    expect([...relatedKeys(events, 'e:loss-1868')].sort()).toEqual(['e:loss-1868', 'w:idiot']);
    expect([...relatedKeys(events, 'w:idiot')].sort()).toEqual(['e:loss-1868', 'w:idiot']);
    expect(relatedKeys(events, null).size).toBe(0);
  });
});
