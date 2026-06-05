import { describe, it, expect } from 'vitest';
import { bySeriesOrder, type BookListItem } from './books';

function bk(id: number, title: string, seriesOrder?: number): BookListItem {
  return { id, title, authors: [], lib_id: String(id), series_order: seriesOrder };
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
