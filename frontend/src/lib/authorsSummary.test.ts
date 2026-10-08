import { describe, expect, it } from 'vitest';
import { pluralAuthors, summarizeAuthors } from './books';

describe('summarizeAuthors (#449)', () => {
  const many = Array.from({ length: 56 }, (_, i) => `Автор${i} Имя`);
  it('десятки авторов — три и «ещё N»', () => {
    expect(summarizeAuthors(many)).toEqual({ shown: many.slice(0, 3), more: 53 });
  });
  it('автор страницы — первым', () => {
    const s = summarizeAuthors(many, 'Автор40 Имя');
    expect(s.shown[0]).toBe('Автор40 Имя');
    expect(s.more).toBe(53);
    expect(summarizeAuthors(['Иванов Иван Петрович', 'Петров Пётр'], 'Петров Пётр').shown[0]).toBe('Петров Пётр');
    expect(summarizeAuthors(['Петров Пётр', 'Иванов Иван Петрович'], 'Иванов Иван').shown[0]).toBe('Иванов Иван Петрович');
  });
  it('одного лишнего не прячем', () => {
    expect(summarizeAuthors(['А', 'Б', 'В', 'Г'])).toEqual({ shown: ['А', 'Б', 'В', 'Г'], more: 0 });
  });
  it('склонение', () => {
    expect([1, 2, 5, 11, 21, 53].map(pluralAuthors)).toEqual(['автор', 'автора', 'авторов', 'авторов', 'автор', 'автора']);
  });
});
