import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { apiFetch } from './api';

// Скрытие авторов и работ на инстансе (админ, миграция 0064): скрытое не
// видит никто, кроме админа — ни в каталоге и поиске, ни в OPDS, ни по ссылке;
// из избранного и с полок пропадает молча. Бэкенд — api/hiding.go.

export type HideKind = 'author' | 'work';
export type HiddenRef = { kind: HideKind; id: number; name: string };
/** Состояние скрытия: explicit — скрыт сам (вернуть здесь), via — из-за кого. */
export type HiddenStatus = { hidden: boolean; explicit: boolean; via: HiddenRef[] };
export type HiddenItem = {
  kind: HideKind;
  id: number;
  name: string;
  note: string;
  hidden_at: string;
  books: number;
};

const base = (kind: HideKind | 'book', id: number) => `/api/admin/${kind}s/${id}`;

/** useHiddenStatus — для админской плашки на карточке (enabled — только админу). */
export function useHiddenStatus(kind: HideKind | 'book', id: number | undefined, enabled: boolean) {
  return useQuery({
    queryKey: ['hidden-status', kind, id],
    queryFn: () => apiFetch<HiddenStatus>(`${base(kind, id ?? 0)}/hidden-status`),
    enabled: enabled && !!id,
  });
}

/** useHiddenList — всё явно скрытое (страница «Скрытое»). */
export function useHiddenList() {
  return useQuery({
    queryKey: ['hidden-list'],
    queryFn: () => apiFetch<{ items: HiddenItem[] }>('/api/admin/hidden'),
  });
}

/** fetchHidePreview — сколько изданий и работ скроет скрытие (текст подтверждения). */
export function fetchHidePreview(kind: HideKind, id: number) {
  return apiFetch<{ books: number; works: number }>(`${base(kind, id)}/hide-preview`);
}

type SetHiddenResult = { hidden: boolean; books_changed: number; works_changed: number };

/** useSetHidden — скрыть (hide=true, с заметкой) или вернуть. */
export function useSetHidden() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { kind: HideKind; id: number; hide: boolean; note?: string }) =>
      apiFetch<SetHiddenResult>(`${base(v.kind, v.id)}/hide`, {
        method: v.hide ? 'POST' : 'DELETE',
        body: v.hide ? { note: v.note ?? '' } : undefined,
      }),
    onSuccess: (r) => {
      // Скрытие меняет почти всё: списки, поиск, карточки, полки, ленты.
      void qc.invalidateQueries();
      toast.success(
        r.hidden
          ? `Скрыто на инстансе: изданий — ${r.books_changed}`
          : `Возвращено: изданий — ${r.books_changed}`,
      );
    },
    onError: (e) => toast.error(`Не удалось: ${e instanceof Error ? e.message : 'ошибка'}`),
  });
}
