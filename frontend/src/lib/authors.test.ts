import { describe, expect, it } from 'vitest';
import { authorYears } from './authors';

describe('authorYears (#579)', () => {
  const now = 2026;
  it('годы жизни, если известны, — вместо годов работ', () => {
    expect(authorYears({ born_year: 1891, died_year: 1940, years_active: { from: 1919, to: 1940 } }, now)).toEqual({
      text: '1891–1940',
      title: 'Годы жизни',
    });
  });
  it('живущий автор — «р. …»; давний без года смерти — годы работ', () => {
    expect(authorYears({ born_year: 1965 }, now)?.text).toBe('р. 1965');
    expect(authorYears({ born_year: 1820, years_active: { from: 1850, to: 1880 } }, now)?.text).toBe('работы 1850–1880');
  });
  it('без годов жизни — явно «работы …»', () => {
    expect(authorYears({ years_active: { from: 1974, to: 2009 } }, now)).toEqual({
      text: 'работы 1974–2009',
      title: 'Годы написания работ в каталоге',
    });
    expect(authorYears({ years_active: { from: 1951, to: 1951 } }, now)?.text).toBe('работы 1951');
  });
  it('только год смерти без годов работ — «ум. …»; ничего — null', () => {
    expect(authorYears({ died_year: 1940 }, now)?.text).toBe('ум. 1940');
    expect(authorYears({}, now)).toBeNull();
  });
});
