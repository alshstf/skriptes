import { describe, expect, it } from 'vitest';
import { reasonParts, reasonText } from './recReasons';
import { formatRelativeDays } from './format';

const now = new Date(2026, 9, 20, 12, 0, 0);
const daysAgo = (n: number) => new Date(2026, 9, 20 - n, 9, 0, 0).toISOString();

describe('formatRelativeDays', () => {
  it('календарные дни и склонения', () => {
    expect(formatRelativeDays(daysAgo(0), now)).toBe('сегодня');
    expect(formatRelativeDays(new Date(2026, 9, 21).toISOString(), now)).toBe('сегодня');
    expect(formatRelativeDays(daysAgo(1), now)).toBe('вчера');
    expect(formatRelativeDays(daysAgo(3), now)).toBe('3 дня назад');
    expect(formatRelativeDays(daysAgo(6), now)).toBe('6 дней назад');
    expect(formatRelativeDays(daysAgo(7), now)).toBe('1 неделю назад');
    expect(formatRelativeDays(daysAgo(20), now)).toBe('2 недели назад');
    expect(formatRelativeDays(daysAgo(31), now)).toBe('1 месяц назад');
    expect(formatRelativeDays(daysAgo(70), now)).toBe('2 месяца назад');
    expect(formatRelativeDays(daysAgo(400), now)).toBe('1 год назад');
    expect(formatRelativeDays(daysAgo(800), now)).toBe('2 года назад');
    expect(formatRelativeDays(daysAgo(2000), now)).toBe('5 лет назад');
    expect(formatRelativeDays('мусор', now)).toBe('');
  });
});

describe('reasonParts', () => {
  it('остановились на …', () => {
    expect(reasonText({ kind: 'in_progress', fraction: 0.424, at: daysAgo(3) }, now)).toBe(
      'Остановились на 42\u00a0% · 3 дня назад',
    );
    expect(reasonText({ kind: 'in_progress', fraction: 1.7 }, now)).toBe('Остановились на 100\u00a0%');
  });

  it('подписка на автора — ссылка на автора', () => {
    const parts = reasonParts(
      { kind: 'new_by_author', author_id: 7, author: 'Стругацкие', at: daysAgo(1) },
      now,
    );
    expect(parts.map((p) => p.text).join('')).toBe('По подписке на автора: Стругацкие · добавлена вчера');
    expect(parts[1].link).toEqual({ to: '/authors/$id', id: 7 });
  });

  it('подписка на цикл — ссылка на цикл, без даты', () => {
    const parts = reasonParts({ kind: 'new_in_series', series_id: 3, series: 'Мир Полудня' }, now);
    expect(reasonText({ kind: 'new_in_series', series_id: 3, series: 'Мир Полудня' }, now)).toBe(
      'По подписке на цикл: Мир Полудня',
    );
    expect(parts[1].link).toEqual({ to: '/series/$id', id: 3 });
  });

  it('скачали / дочитали', () => {
    expect(reasonText({ kind: 'acquired', at: daysAgo(45) }, now)).toBe('Скачали 1 месяц назад');
    expect(reasonText({ kind: 'acquired' }, now)).toBe('Скачали');
    expect(reasonText({ kind: 'finished', at: daysAgo(2) }, now)).toBe('Дочитали 2 дня назад');
  });

  it('неизвестный вид — пусто', () => {
    expect(reasonParts({ kind: 'nope' } as unknown as Parameters<typeof reasonParts>[0], now)).toEqual([]);
  });
});

describe('formatReadingTime', () => {
  it('минуты с шагом 5, часы с шагом 10 минут', async () => {
    const { formatReadingTime } = await import('./format');
    expect(formatReadingTime(0)).toBe('');
    expect(formatReadingTime(3)).toBe('≈ 5 мин');
    expect(formatReadingTime(42)).toBe('≈ 40 мин');
    expect(formatReadingTime(60)).toBe('≈ 1 ч');
    expect(formatReadingTime(84)).toBe('≈ 1 ч 20 мин');
    expect(formatReadingTime(244)).toBe('≈ 4 ч');
  });
  it('минуты в причине «остановились»', () => {
    expect(reasonText({ kind: 'in_progress', fraction: 0.4, minutes: 84 }, now)).toBe(
      'Остановились на 40 % · ещё ≈ 1 ч 20 мин',
    );
  });
});

describe('searched_appeared', () => {
  it('«вы искали …» с датой', () => {
    expect(reasonText({ kind: 'searched_appeared', query: 'пикник на обочине', at: daysAgo(10) }, now)).toBe(
      'Вы искали «пикник на обочине» · 1 неделю назад',
    );
  });
});

// Правила фазы 2 (план recsys/02): причина называет основание («дочитали X»)
// и ведёт ссылкой на цикл/автора.
describe('reasonParts: правила фазы 2', () => {
  it('next_in_series — номер в цикле и ссылка на цикл', () => {
    const parts = reasonParts(
      {
        kind: 'next_in_series',
        series_id: 7,
        series: 'Ведьмак',
        work_id: 101,
        title: 'Последнее желание',
        ser_no: 2,
      },
      now,
    );
    expect(parts.map((p) => p.text).join('')).toBe('Дочитали «Последнее желание» · № 2 в цикле Ведьмак');
    expect(parts[1].link).toEqual({ to: '/series/$id', id: 7 });
  });

  it('next_in_series без номера — «дальше в цикле»', () => {
    expect(
      reasonText({ kind: 'next_in_series', series_id: 7, series: 'Ведьмак', work_id: 1, title: 'X' }, now),
    ).toBe('Дочитали «X» · дальше в цикле Ведьмак');
  });

  it('author_you_finished — основание и ссылка на автора', () => {
    const parts = reasonParts(
      { kind: 'author_you_finished', author_id: 5, author: 'Лем Станислав', work_id: 42, title: 'Солярис' },
      now,
    );
    expect(parts.map((p) => p.text).join('')).toBe('Дочитали «Солярис» · автор: Лем Станислав');
    expect(parts[1].link).toEqual({ to: '/authors/$id', id: 5 });
  });
});

// Второй минор фазы 2: брошенное, перечитать, забытая классика.
describe('reasonParts: брошенное, перечитать, классика', () => {
  it('dropped_then_loved_author — давность и число полюбленных книг, ссылка на автора', () => {
    const parts = reasonParts(
      { kind: 'dropped_then_loved_author', author_id: 3, author: 'Лем Станислав', count: 2, at: daysAgo(400) },
      now,
    );
    expect(parts.map((p) => p.text).join('')).toBe('Отложили 1 год назад · с тех пор 2 книги автора: Лем Станислав');
    expect(parts[1].link).toEqual({ to: '/authors/$id', id: 3 });
    expect(
      reasonText({ kind: 'dropped_then_loved_author', author_id: 3, author: 'X', count: 5 }, now),
    ).toBe('Отложили · с тех пор 5 книг автора: X');
  });

  it('reread — оценка 5 и давность', () => {
    expect(reasonText({ kind: 'reread', at: daysAgo(1500) }, now)).toBe('Поставили 5 · читали 4 года назад · перечитать?');
    expect(reasonText({ kind: 'reread' }, now)).toBe('Поставили 5 · перечитать?');
  });

  it('forgotten_classic — год, рейтинг по источнику, «никто не читал»', () => {
    expect(reasonText({ kind: 'forgotten_classic', year: 1965, rating: 9.42, source: 'fantlab' }, now)).toBe(
      '1965 год · Фантлаб 9.4 · здесь ещё никто не читал',
    );
    expect(reasonText({ kind: 'forgotten_classic', year: 1930, rating: 5, source: 'librate' }, now)).toBe(
      '1930 год · рейтинг 5/5 · здесь ещё никто не читал',
    );
    expect(reasonText({ kind: 'forgotten_classic' }, now)).toBe('Классика · здесь ещё никто не читал');
  });
});
