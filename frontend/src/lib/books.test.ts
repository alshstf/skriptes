import { describe, it, expect } from 'vitest';
import { bySeriesOrder, computeMergeSuggestions, type BookListItem } from './books';

function bk(id: number, title: string, seriesOrder?: number): BookListItem {
  return { id, title, authors: [], lib_id: String(id), series_order: seriesOrder };
}

function mb(p: Partial<BookListItem>): BookListItem {
  return { id: 0, title: '', authors: [], lib_id: '', ...p };
}

describe('bySeriesOrder', () => {
  it('sorts by series_order ascending', () => {
    const arr = [bk(1, 'C', 2), bk(2, 'A', 0), bk(3, 'B', 1)];
    const ids = [...arr].sort(bySeriesOrder).map((b) => b.id);
    expect(ids).toEqual([2, 3, 1]);
  });

  it('books without series_order go last, tie-broken by title (ru)', () => {
    const arr = [bk(1, 'Яблоко'), bk(2, 'Аист'), bk(3, 'С номером', 0)];
    const ids = [...arr].sort(bySeriesOrder).map((b) => b.id);
    expect(ids).toEqual([3, 2, 1]); // 0 first; then Аист < Яблоко
  });

  it('stable for equal order via title tiebreak', () => {
    const arr = [bk(1, 'Бета', 5), bk(2, 'Альфа', 5)];
    const ids = [...arr].sort(bySeriesOrder).map((b) => b.id);
    expect(ids).toEqual([2, 1]);
  });
});

describe('computeMergeSuggestions', () => {
  it('один ser_no с двумя разными работами → подсказка (кейс Страйка)', () => {
    const books = [
      mb({ id: 1, work_id: 100, ser_no: 7, title: 'Развороченная могила' }),
      mb({ id: 2, work_id: 200, ser_no: 7, title: 'Неизбежная могила' }),
      mb({ id: 3, work_id: 300, ser_no: 6, title: 'Чернильно-чёрное сердце' }),
    ];
    const s = computeMergeSuggestions(books);
    expect(s).toHaveLength(1);
    expect(s[0].serNo).toBe(7);
    expect([...s[0].workIds].sort((a, b) => a - b)).toEqual([100, 200]);
    expect(s[0].books).toHaveLength(2);
  });

  it('один ser_no с одной работой (уже схлопнуто) → нет подсказки', () => {
    const books = [mb({ id: 1, work_id: 100, ser_no: 1 }), mb({ id: 2, work_id: 200, ser_no: 2 })];
    expect(computeMergeSuggestions(books)).toHaveLength(0);
  });

  it('ser_no=0 / отсутствует не учитывается', () => {
    const books = [
      mb({ id: 1, work_id: 100, ser_no: 0 }),
      mb({ id: 2, work_id: 200, ser_no: 0 }),
      mb({ id: 3, work_id: 300 }),
      mb({ id: 4, work_id: 400 }),
    ];
    expect(computeMergeSuggestions(books)).toHaveLength(0);
  });

  it('work_id отсутствует → fallback на id', () => {
    const books = [mb({ id: 11, ser_no: 3 }), mb({ id: 22, ser_no: 3 })];
    const s = computeMergeSuggestions(books);
    expect(s).toHaveLength(1);
    expect([...s[0].workIds].sort((a, b) => a - b)).toEqual([11, 22]);
  });

  it('несколько подсказок отсортированы по ser_no', () => {
    const books = [
      mb({ id: 1, work_id: 1, ser_no: 5 }),
      mb({ id: 2, work_id: 2, ser_no: 5 }),
      mb({ id: 3, work_id: 3, ser_no: 2 }),
      mb({ id: 4, work_id: 4, ser_no: 2 }),
    ];
    expect(computeMergeSuggestions(books).map((s) => s.serNo)).toEqual([2, 5]);
  });
});
