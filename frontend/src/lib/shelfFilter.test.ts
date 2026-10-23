import { describe, expect, it } from 'vitest';
import { filterStatus, matchesShelfFilter, normalizeForFilter } from './shelfFilter';
import { withShelfQuery } from './smartShelves';

describe('matchesShelfFilter', () => {
  const book = { title: 'Скотный двор', authors: ['Оруэлл Джордж'], series: 'Антиутопии' };

  it('пустой запрос подходит всем', () => {
    expect(matchesShelfFilter('', book)).toBe(true);
    expect(matchesShelfFilter('   ', book)).toBe(true);
  });

  it('ищет по названию, автору и серии без учёта регистра', () => {
    expect(matchesShelfFilter('скот', book)).toBe(true);
    expect(matchesShelfFilter('ОРУЭЛЛ', book)).toBe(true);
    expect(matchesShelfFilter('антиутоп', book)).toBe(true);
    expect(matchesShelfFilter('дюна', book)).toBe(false);
  });

  it('каждое слово запроса должно найтись', () => {
    expect(matchesShelfFilter('оруэлл двор', book)).toBe(true);
    expect(matchesShelfFilter('оруэлл дюна', book)).toBe(false);
  });

  it('ё и е не различаются', () => {
    expect(matchesShelfFilter('ежик', { title: 'Ёжик в тумане', authors: [] })).toBe(true);
    expect(matchesShelfFilter('Алексеев Артёмович', { title: 'Х', authors: ['Алексеев Евгений Артемович'] })).toBe(true);
  });

  it('normalizeForFilter схлопывает пробелы', () => {
    expect(normalizeForFilter('  Скотный   Двор ')).toBe('скотный двор');
  });
});

describe('filterStatus', () => {
  it('считает найденное', () => {
    expect(filterStatus(3, 42)).toBe('Найдено 3 из 42');
    expect(filterStatus(0, 42)).toBe('Ничего не нашлось');
  });
});

describe('withShelfQuery', () => {
  it('дописывает текст к поиску полки и не трогает остальные фильтры', () => {
    expect(withShelfQuery({ genres: ['sf'], unread: true }, 'дюна')).toEqual({ genres: ['sf'], unread: true, q: 'дюна' });
    expect(withShelfQuery({ q: 'стругацкие' }, ' пикник ')).toEqual({ q: 'стругацкие пикник' });
  });
});
