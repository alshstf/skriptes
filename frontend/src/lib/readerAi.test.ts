import { describe, expect, it } from 'vitest';
import { isShortSelection, takeEvents } from './readerAi';

describe('takeEvents', () => {
  it('отдаёт целые события и оставляет неполный хвост', () => {
    let rest = '';
    const got = takeEvents(
      'data: {"kind":"stage","stage":1}\n\ndata: {"kind":"delta","text":"При"}\n\ndata: {"kind":"del',
      (r) => (rest = r),
    );
    expect(got).toEqual([
      { kind: 'stage', stage: 1 },
      { kind: 'delta', text: 'При' },
    ]);
    expect(rest).toBe('data: {"kind":"del');
    expect(takeEvents(rest + 'ta","text":"вет"}\n\n', (r) => (rest = r))).toEqual([{ kind: 'delta', text: 'вет' }]);
    expect(rest).toBe('');
  });

  it('пропускает битые строки и не-события', () => {
    let rest = 'x';
    expect(takeEvents(': ping\n\ndata: {oops\n\ndata: 5\n\n', (r) => (rest = r))).toEqual([]);
    expect(rest).toBe('');
  });
});

describe('isShortSelection', () => {
  it('имя или пара слов — да, предложение — нет', () => {
    expect(isShortSelection('Анна Павловна')).toBe(true);
    expect(isShortSelection(' фрейлина ')).toBe(true);
    expect(isShortSelection('Где письмо? — спросил Пётр, и Анна побледнела.')).toBe(false);
    expect(isShortSelection('')).toBe(false);
  });
});
