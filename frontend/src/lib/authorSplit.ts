import type { Author } from '@/lib/catalog';

/** Строка выбора работы в диалоге разделения автора (#356). */
export type WorkRow = { workId: number; title: string; year?: number; series?: string };

/** Работы автора по одной на work_id, по году (без года — в конце). */
export function authorWorks(author: Pick<Author, 'books'>): WorkRow[] {
  const seen = new Map<number, WorkRow>();
  for (const b of author.books ?? []) {
    const id = b.work_id ?? b.id;
    if (!seen.has(id)) seen.set(id, { workId: id, title: b.title, year: b.year, series: b.series });
  }
  return [...seen.values()].sort(
    (a, b) => (a.year ?? 9999) - (b.year ?? 9999) || a.title.localeCompare(b.title, 'ru'),
  );
}
