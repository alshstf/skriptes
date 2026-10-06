import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from './api';

// Закладки и выделения с заметками веб-ридера (#389).

export type Annotation = {
  id: number;
  book_id: number;
  kind: 'bookmark' | 'highlight';
  cfi: string;
  excerpt?: string;
  note?: string;
  label?: string;
  fraction?: number;
  created_at: string;
  updated_at: string;
  edition_title?: string;
  edition_lang?: string;
};

export type NewAnnotation = Pick<Annotation, 'kind' | 'cfi'> &
  Partial<Pick<Annotation, 'excerpt' | 'note' | 'label' | 'fraction'>>;

const bookKey = (bookId: number) => ['annotations', 'book', bookId] as const;

/** useBookAnnotations — заметки в издании (ридер). */
export function useBookAnnotations(bookId: number) {
  return useQuery({
    queryKey: bookKey(bookId),
    queryFn: async ({ signal }) =>
      (await apiFetch<{ items: Annotation[] }>(`/api/books/${bookId}/annotations`, { signal })).items,
    enabled: bookId > 0,
    staleTime: 60_000,
  });
}

/** useWorkAnnotations — «Мои заметки» на карточке: все издания работы. */
export function useWorkAnnotations(workId: number | undefined) {
  return useQuery({
    queryKey: ['annotations', 'work', workId ?? 0],
    queryFn: async ({ signal }) =>
      (await apiFetch<{ items: Annotation[] }>(`/api/works/${workId}/annotations`, { signal })).items,
    enabled: (workId ?? 0) > 0,
    staleTime: 60_000,
  });
}

function useInvalidate() {
  const qc = useQueryClient();
  return () => void qc.invalidateQueries({ queryKey: ['annotations'] });
}

export function useSaveAnnotation(bookId: number) {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (a: NewAnnotation) =>
      apiFetch<Annotation>(`/api/books/${bookId}/annotations`, { method: 'POST', body: a }),
    onSuccess: invalidate,
  });
}

export function useUpdateAnnotationNote() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: ({ id, note }: { id: number; note: string }) =>
      apiFetch<Annotation>(`/api/annotations/${id}`, { method: 'PATCH', body: { note } }),
    onSuccess: invalidate,
  });
}

export function useDeleteAnnotation() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (id: number) => apiFetch<void>(`/api/annotations/${id}`, { method: 'DELETE' }),
    onSuccess: invalidate,
  });
}

/** placeLabel — «Глава 3 · 42 %» для списка. */
export function placeLabel(a: Pick<Annotation, 'label' | 'fraction'>): string {
  const parts = [];
  if (a.label) parts.push(a.label);
  if (typeof a.fraction === 'number') parts.push(`${Math.round(a.fraction * 100)} %`);
  return parts.join(' · ');
}
