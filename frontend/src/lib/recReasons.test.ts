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
      'Остановились на 42 % · 3 дня назад',
    );
    expect(reasonText({ kind: 'in_progress', fraction: 1.7 }, now)).toBe('Остановились на 100 %');
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
