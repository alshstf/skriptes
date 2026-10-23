import { useCallback, useState } from 'react';

// Фильтр по названию у раскрытой полки /shelves (#582): личные полки, «Избранное»,
// «Хочу прочитать», подборки — фильтр по уже загруженному списку (без запроса),
// умные полки — поиском по всей полке на сервере (раскрыто только превью).

/** SHELF_FILTER_MIN — с какого числа книг у полки появляется фильтр. */
export const SHELF_FILTER_MIN = 8;
/** SHELF_FILTER_STICKY_MIN — с какого числа книг фильтр закреплён под шапкой при скролле. */
export const SHELF_FILTER_STICKY_MIN = 20;

/** normalizeForFilter — нижний регистр, ё → е, схлопнутые пробелы. */
export function normalizeForFilter(s: string): string {
  return s.toLocaleLowerCase('ru-RU').replace(/ё/g, 'е').replace(/\s+/g, ' ').trim();
}

/**
 * matchesShelfFilter — подходит ли книга под фильтр: каждое слово запроса
 * найдено в названии, авторах или серии («оруэлл скотный» → «Скотный двор»
 * Оруэлла). Пустой запрос подходит всем.
 */
export function matchesShelfFilter(
  query: string,
  book: { title: string; authors?: string[]; series?: string },
): boolean {
  const q = normalizeForFilter(query);
  if (!q) return true;
  const hay = normalizeForFilter([book.title, ...(book.authors ?? []), book.series ?? ''].join(' '));
  return q.split(' ').every((w) => hay.includes(w));
}

// Текст фильтра живёт в памяти страницы (не в URL): «Назад» с карточки книги
// возвращает полку раскрытой (?open=…) и с тем же фильтром, а перезагрузка — чистой.
const memory = new Map<string, string>();

/** useShelfFilter — текст фильтра полки key (как в ?open=: «c:5», «p:adaptations», «s:12»). */
export function useShelfFilter(key: string): [string, (v: string) => void] {
  const [value, setValue] = useState(() => memory.get(key) ?? '');
  const set = useCallback(
    (v: string) => {
      if (v) memory.set(key, v);
      else memory.delete(key);
      setValue(v);
    },
    [key],
  );
  return [value, set];
}

/** filterStatus — «Найдено 3 из 42» / «Ничего не нашлось». */
export function filterStatus(found: number, total: number): string {
  return found === 0
    ? 'Ничего не нашлось'
    : `Найдено ${found.toLocaleString('ru-RU')} из ${total.toLocaleString('ru-RU')}`;
}
