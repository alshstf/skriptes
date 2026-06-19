/**
 * lib/collections.ts — клиентские хуки личных полок (раздел «Жанры»).
 *
 * Полка (collection) — пользовательский именованный список книг. CRUD полки +
 * членство книг. Мутации инвалидируют кэш списка полок (и кэш книг конкретной
 * полки) + показывают тост, как в lib/admin.ts.
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { apiFetch } from './api';

export type Collection = {
  id: number;
  name: string;
  book_count: number;
  created_at: string;
  updated_at: string;
};

/**
 * CollectionBook — книга в полке. work_id ведёт карточку на /works/{work_id ?? id}
 * (как в каталоге, см. граблю №15: ссылки discovery → works).
 */
export type CollectionBook = {
  id: number;
  title: string;
  authors: string[];
  series?: string;
  lang?: string;
  work_id?: number;
  added_at: string;
};

const KEY = ['me', 'collections'] as const;

/** queryKey книг конкретной полки. */
function booksKey(id: number) {
  return [...KEY, id, 'books'] as const;
}

/** queryKey: полки, содержащие конкретную книгу (членство для карточки). */
function bookShelvesKey(bookId: number) {
  return [...KEY, 'by-book', bookId] as const;
}

/** BookShelf — лёгкая полка (id+name), содержащая книгу. Для индикации на карточке. */
export type BookShelf = { id: number; name: string };

type ListCollectionsResponse = { items: Collection[] };
type ListBooksResponse = { items: CollectionBook[] };
type BookShelvesResponse = { items: BookShelf[] };

/**
 * useBookCollections — полки, в которых лежит книга (издание book.id). Питает
 * индикацию членства на карточке («На полках: …») и стейт кнопки «На полку».
 */
export function useBookCollections(bookId: number | undefined) {
  return useQuery<BookShelf[]>({
    queryKey: bookId != null ? bookShelvesKey(bookId) : [...KEY, 'by-book', 'none'],
    queryFn: async () => {
      const r = await apiFetch<BookShelvesResponse>(`/api/books/${bookId}/collections`);
      return r.items;
    },
    enabled: bookId != null,
    staleTime: 30_000,
  });
}

/** useCollections — список полок текущего пользователя. */
export function useCollections() {
  return useQuery<Collection[]>({
    queryKey: [...KEY],
    queryFn: async () => {
      const r = await apiFetch<ListCollectionsResponse>('/api/me/collections');
      return r.items;
    },
    staleTime: 30_000,
  });
}

/**
 * useCollectionBooks — книги одной полки. enabled управляет ленивой загрузкой:
 * грузим только когда полка раскрыта (id != null).
 */
export function useCollectionBooks(id: number | null) {
  return useQuery<CollectionBook[]>({
    queryKey: id != null ? booksKey(id) : [...KEY, 'books', 'none'],
    queryFn: async () => {
      const r = await apiFetch<ListBooksResponse>(`/api/me/collections/${id}`);
      return r.items;
    },
    enabled: id != null,
    staleTime: 30_000,
  });
}

export function useCreateCollection() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) =>
      apiFetch<Collection>('/api/me/collections', { method: 'POST', body: { name } }),
    onSuccess: (c) => {
      void qc.invalidateQueries({ queryKey: [...KEY] });
      toast.success(`Полка «${c.name}» создана`);
    },
    onError: () => toast.error('Не удалось создать полку'),
  });
}

export function useRenameCollection() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { id: number; name: string }) =>
      apiFetch<void>(`/api/me/collections/${vars.id}`, {
        method: 'PATCH',
        body: { name: vars.name },
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: [...KEY] });
      toast.success('Полка переименована');
    },
    onError: () => toast.error('Не удалось переименовать полку'),
  });
}

export function useDeleteCollection() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) =>
      apiFetch<void>(`/api/me/collections/${id}`, { method: 'DELETE' }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: [...KEY] });
      toast.success('Полка удалена');
    },
    onError: () => toast.error('Не удалось удалить полку'),
  });
}

export function useAddBookToCollection() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { collectionId: number; bookId: number }) =>
      apiFetch<void>(`/api/me/collections/${vars.collectionId}/books/${vars.bookId}`, {
        method: 'POST',
      }),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: [...KEY] });
      void qc.invalidateQueries({ queryKey: booksKey(vars.collectionId) });
      void qc.invalidateQueries({ queryKey: bookShelvesKey(vars.bookId) });
      toast.success('Добавлено на полку');
    },
    onError: () => toast.error('Не удалось добавить на полку'),
  });
}

export function useRemoveBookFromCollection() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { collectionId: number; bookId: number }) =>
      apiFetch<void>(`/api/me/collections/${vars.collectionId}/books/${vars.bookId}`, {
        method: 'DELETE',
      }),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: [...KEY] });
      void qc.invalidateQueries({ queryKey: booksKey(vars.collectionId) });
      void qc.invalidateQueries({ queryKey: bookShelvesKey(vars.bookId) });
      toast.success('Убрано с полки');
    },
    onError: () => toast.error('Не удалось убрать с полки'),
  });
}
