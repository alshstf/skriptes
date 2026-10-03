import { describe, expect, it } from 'vitest';
import { authorWorks } from '@/lib/authorSplit';
import type { BookListItem } from '@/lib/books';

const book = (id: number, title: string, year?: number, work_id?: number) =>
  ({ id, title, year, work_id }) as unknown as BookListItem;

describe('authorWorks', () => {
  it('одна строка на работу, по году, без года — в конце', () => {
    const rows = authorWorks({
      books: [
        book(1, 'Ночная смена', 2010, 10),
        book(2, 'Гладиатор', 1850, 20),
        book(3, 'Ночная смена (другое издание)', 2010, 10),
        book(4, 'Методичка'),
      ],
    });
    expect(rows.map((r) => r.workId)).toEqual([20, 10, 4]);
    expect(rows[0]).toMatchObject({ title: 'Гладиатор', year: 1850 });
  });
});
