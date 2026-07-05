/**
 * lib/content.ts — клиентские хуки раздела «Контент» (видимость книг по
 * языкам/жанрам).
 *
 * Три потребителя:
 *  - панель фильтров /books — useEffectiveContent (admin ∪ персональные
 *    скрытые), чтобы не показывать скрытые опции;
 *  - админка /admin/content — useAdminContent (глобально для всех);
 *  - профиль /me/content — useMyContent (персонально, admin-скрытые
 *    приходят как read-only locked).
 *
 * useLanguages — полный список языков коллекции (для обоих разделов
 * «Контент»; НЕ фильтруется по скрытым — их как раз надо показать).
 */

import { useMemo } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from './api';

export type LanguageItem = {
  code: string;
  display: string;
  book_count: number;
};

export type ContentSettings = {
  hidden_genres: string[];
  hidden_languages: string[];
  // «Скрывать сборники» (антологии/тома собраний) из выдачи целиком —
  // opt-in персональная настройка (дефолт false). Только в профиле,
  // admin-эндпоинт поле игнорирует.
  hide_compilations?: boolean;
};

export type MyContentSettings = ContentSettings & {
  admin_hidden_genres: string[];
  admin_hidden_languages: string[];
};

type LanguagesResponse = { items: LanguageItem[] };

/** sameSet — сравнение двух наборов кодов без учёта порядка (для dirty-флага). */
export function sameSet(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false;
  const set = new Set(a);
  return b.every((x) => set.has(x));
}

/**
 * useLanguages — все языки коллекции с числом книг (для разделов
 * «Контент»). staleTime 5 минут: множество языков меняется только при
 * импорте новой коллекции.
 */
export function useLanguages() {
  return useQuery<LanguageItem[]>({
    queryKey: ['languages'],
    queryFn: async () => {
      const r = await apiFetch<LanguagesResponse>('/api/languages');
      return r.items;
    },
    staleTime: 5 * 60_000,
  });
}

/**
 * useLanguageMap — производный Map код→display (как useGenreMap для жанров).
 * Нужен там, где есть только код языка (фасеты `/books`, чипы активных
 * фильтров): резолвим «ru» → «Русский». Пустой Map пока запрос не завершён
 * — caller фолбэкает на сам код.
 */
export function useLanguageMap(): Map<string, string> {
  const q = useLanguages();
  const map = new Map<string, string>();
  for (const l of q.data ?? []) map.set(l.code, l.display);
  return map;
}

/**
 * useSrcLanguages — ЯЗЫКИ ОРИГИНАЛА коллекции (books.src_lang из fb2) с числом
 * книг: опции фильтра «Язык оригинала» в разделе «Авторы» + display-имена для
 * src-фасета /books (язык оригинала может отсутствовать среди языков ИЗДАНИЙ —
 * useLanguageMap его не резолвит).
 */
export function useSrcLanguages() {
  return useQuery<LanguageItem[]>({
    queryKey: ['languages', 'src'],
    queryFn: async () => {
      const r = await apiFetch<LanguagesResponse>('/api/languages?src=1');
      return r.items;
    },
    staleTime: 5 * 60_000,
  });
}

/** useSrcLanguageMap — Map код→display для языков оригинала (зеркало useLanguageMap). */
export function useSrcLanguageMap(): Map<string, string> {
  const q = useSrcLanguages();
  const map = new Map<string, string>();
  for (const l of q.data ?? []) map.set(l.code, l.display);
  return map;
}

// ISO 639-1 — все 2-буквенные коды языков. Имена резолвим через Intl.DisplayNames
// на язык интерфейса (ru), без хардкода. Импорт нормализует lang именно до 2 букв
// (грабля №14), поэтому этот набор полный и достаточный.
const ISO_639_1 =
  'aa ab ae af ak am an ar as av ay az ba be bg bh bi bm bn bo br bs ca ce ch co cr cs cu cv cy da de dv dz ee el en eo es et eu fa ff fi fj fo fr fy ga gd gl gn gu gv ha he hi ho hr ht hu hy hz ia id ie ig ii ik io is it iu ja jv ka kg ki kj kk kl km kn ko kr ks ku kv kw ky la lb lg li ln lo lt lu lv mg mh mi mk ml mn mr ms mt my na nb nd ne ng nl nn no nr nv ny oc oj om or os pa pi pl ps pt qu rm rn ro ru rw sa sc sd se sg si sk sl sm sn so sq sr ss st su sv sw ta te tg th ti tk tl tn to tr ts tt tw ty ug uk ur uz ve vi vo wa wo xh yi yo za zh zu'.split(
    ' ',
  );

/**
 * useLanguageOptions — ПОЛНЫЙ список языков для правки языка издания (оверрайд):
 * весь ISO 639-1, а не только присутствующие в коллекции (мислейбл правят на язык,
 * которого в инстансе ещё нет). Имена: backend-display коллекции (приоритет) →
 * Intl.DisplayNames('ru') → сам код. Отсортирован по имени.
 */
export function useLanguageOptions(): { code: string; display: string }[] {
  const q = useLanguages();
  const data = q.data;
  return useMemo(() => {
    const backend = new Map<string, string>();
    for (const l of data ?? []) backend.set(l.code, l.display);
    let dn: Intl.DisplayNames | null = null;
    try {
      dn = new Intl.DisplayNames(['ru'], { type: 'language' });
    } catch {
      dn = null;
    }
    const seen = new Set<string>();
    const out: { code: string; display: string }[] = [];
    for (const code of [...backend.keys(), ...ISO_639_1]) {
      if (seen.has(code)) continue;
      seen.add(code);
      out.push({ code, display: backend.get(code) ?? dn?.of(code) ?? code });
    }
    return out.sort((a, b) => a.display.localeCompare(b.display, 'ru'));
  }, [data]);
}

/**
 * useEffectiveContent — объединённый набор скрытого (admin ∪ персональное)
 * для текущего пользователя. Панель фильтров прячет эти жанры/языки.
 * Бэкенд уже исключает их из выдачи — это только для чистого UI.
 */
export function useEffectiveContent() {
  return useQuery<ContentSettings>({
    queryKey: ['content', 'effective'],
    queryFn: () => apiFetch<ContentSettings>('/api/content/effective'),
    staleTime: 60_000,
  });
}

const ADMIN_KEY = ['admin', 'content'] as const;

export function useAdminContent() {
  return useQuery<ContentSettings>({
    queryKey: [...ADMIN_KEY],
    queryFn: () => apiFetch<ContentSettings>('/api/admin/content'),
    staleTime: 30_000,
  });
}

export function useUpdateAdminContent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: ContentSettings) =>
      apiFetch<ContentSettings>('/api/admin/content', { method: 'PUT', body: vars }),
    onSuccess: (data) => {
      qc.setQueryData([...ADMIN_KEY], data);
      // Глобальные изменения влияют на выдачу всех пользователей.
      qc.invalidateQueries({ queryKey: ['content', 'effective'] });
    },
  });
}

const ME_KEY = ['me', 'content'] as const;

export function useMyContent() {
  return useQuery<MyContentSettings>({
    queryKey: [...ME_KEY],
    queryFn: () => apiFetch<MyContentSettings>('/api/me/content'),
    staleTime: 30_000,
  });
}

export function useUpdateMyContent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: ContentSettings) =>
      apiFetch<MyContentSettings>('/api/me/content', { method: 'PUT', body: vars }),
    onSuccess: (data) => {
      qc.setQueryData([...ME_KEY], data);
      qc.invalidateQueries({ queryKey: ['content', 'effective'] });
    },
  });
}
