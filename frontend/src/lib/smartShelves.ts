import { useCallback } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { apiFetch } from './api';
import { BOOK_KIND_LABELS, buildBooksParams, type BookFilters, type BookListResponse } from './books';
import { collapseGenreChips, useGenreMap, useGenres } from './genres';
import { useLanguageMap, useSrcLanguageMap } from './content';
import type { BooksSearch } from '@/router';

// Умные полки (#389): сохранённые фильтры каталога /books. Книг не хранят —
// состав считает тот же GET /api/books, поэтому полка обновляется сама.

/** SmartFilters — фильтры полки; ключи — как в URL /books (BooksSearch без page). */
export type SmartFilters = Omit<BooksSearch, 'page'>;

export type SmartShelf = {
  id: number;
  name: string;
  filters: SmartFilters;
  created_at: string;
  updated_at: string;
};

const KEY = ['me', 'smart-shelves'] as const;

/** toSmartFilters — фильтры /books без пустых значений и номера страницы. */
export function toSmartFilters(search: BooksSearch): SmartFilters {
  const out: SmartFilters = {};
  if (search.q) out.q = search.q;
  if (search.genres?.length) out.genres = search.genres;
  if (search.lang) out.lang = search.lang;
  if (search.src_lang) out.src_lang = search.src_lang;
  if (search.kind) out.kind = search.kind;
  if (search.year_from) out.year_from = search.year_from;
  if (search.year_to) out.year_to = search.year_to;
  if (search.series_id) out.series_id = search.series_id;
  if (search.author_id) out.author_id = search.author_id;
  if (search.sort) out.sort = search.sort;
  if (search.unread) out.unread = true;
  return out;
}

/** hasSmartFilters — есть ли что сохранять (весь каталог — не полка). */
export function hasSmartFilters(f: SmartFilters): boolean {
  return Object.keys(toSmartFilters(f)).some((k) => k !== 'sort');
}

/** toBookFilters — фильтры полки → параметры запроса списка книг. */
export function toBookFilters(f: SmartFilters): BookFilters {
  return {
    query: f.q ?? '',
    genres: f.genres,
    lang: f.lang,
    srcLang: f.src_lang,
    kind: f.kind,
    yearFrom: f.year_from,
    yearTo: f.year_to,
    seriesId: f.series_id,
    authorId: f.author_id,
    sort: f.sort,
    unread: f.unread,
  };
}

/** useSmartShelves — умные полки пользователя. */
export function useSmartShelves() {
  return useQuery<SmartShelf[]>({
    queryKey: [...KEY],
    queryFn: async () => (await apiFetch<{ items: SmartShelf[] }>('/api/me/smart-shelves')).items,
    staleTime: 60_000,
  });
}

/** useSmartShelfBooks — первые книги полки и их общее число. */
export function useSmartShelfBooks(filters: SmartFilters, limit: number, enabled = true) {
  return useQuery<BookListResponse>({
    queryKey: ['books-smart', filters, limit],
    queryFn: ({ signal }) =>
      apiFetch<BookListResponse>(`/api/books?${buildBooksParams(toBookFilters(filters), limit, 0)}`, { signal }),
    enabled,
    staleTime: 60_000,
  });
}

export function useCreateSmartShelf() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { name: string; filters: SmartFilters }) =>
      apiFetch<SmartShelf>('/api/me/smart-shelves', { method: 'POST', body: vars }),
    onSuccess: (sh) => {
      void qc.invalidateQueries({ queryKey: [...KEY] });
      toast.success(`Умная полка «${sh.name}» сохранена — она на странице «Мои полки»`);
    },
    onError: () => toast.error('Не удалось сохранить полку'),
  });
}

export function useRenameSmartShelf() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { id: number; name: string }) =>
      apiFetch<SmartShelf>(`/api/me/smart-shelves/${vars.id}`, { method: 'PATCH', body: { name: vars.name } }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: [...KEY] });
      toast.success('Полка переименована');
    },
    onError: () => toast.error('Не удалось переименовать полку'),
  });
}

export function useDeleteSmartShelf() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => apiFetch<void>(`/api/me/smart-shelves/${id}`, { method: 'DELETE' }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: [...KEY] });
      toast.success('Полка удалена');
    },
    onError: () => toast.error('Не удалось удалить полку'),
  });
}

/**
 * useDescribeFilters — фильтры словами: «Фантастика · русский · непрочитанные».
 * Нужен и для подписи полки, и для имени по умолчанию.
 */
export function useDescribeFilters(): (f: SmartFilters) => string {
  const genreMap = useGenreMap();
  const allGenres = useGenres().data;
  const langMap = useLanguageMap();
  const srcLangMap = useSrcLanguageMap();
  return useCallback(
    (f: SmartFilters) => {
      const parts: string[] = [];
      if (f.q) parts.push(`«${f.q}»`);
      if (f.genres?.length) {
        const { fullCategories, rest } = collapseGenreChips(f.genres, allGenres ?? []);
        for (const c of fullCategories) parts.push(c.name);
        for (const g of rest) parts.push(genreMap.get(g)?.display ?? g);
      }
      if (f.lang) parts.push((langMap.get(f.lang) ?? f.lang).toLowerCase());
      if (f.src_lang) parts.push(`оригинал: ${(srcLangMap.get(f.src_lang) ?? langMap.get(f.src_lang) ?? f.src_lang).toLowerCase()}`);
      if (f.kind) parts.push(BOOK_KIND_LABELS[f.kind].toLowerCase());
      if (f.year_from && f.year_to) parts.push(`${f.year_from}–${f.year_to}`);
      else if (f.year_from) parts.push(`с ${f.year_from}`);
      else if (f.year_to) parts.push(`до ${f.year_to}`);
      if (f.series_id) parts.push('одна серия');
      if (f.author_id) parts.push('один автор');
      if (f.unread) parts.push('непрочитанные');
      if (f.sort === 'year_desc') parts.push('сначала новые');
      if (f.sort === 'year_asc') parts.push('сначала старые');
      return parts.join(' · ');
    },
    [genreMap, allGenres, langMap, srcLangMap],
  );
}
