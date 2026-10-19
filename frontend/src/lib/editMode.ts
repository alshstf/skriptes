import { useSyncExternalStore } from 'react';
import { useMe } from './auth';

// Режим правки каталога (#444): контролы правки (поля, жанры, авторы, серия,
// пересборка и слияние изданий, «служебный автор», скрытие на инстансе) админ
// видит только когда включил «Править» на карточке — иначе карточка как у
// читателя. Режим — у той карточки, где его включили (владелец 2026-10-19):
// ушёл с неё («Назад», на другую книгу или автора) — режим выключен, вернулся —
// тоже выключен. В sessionStorage — путь карточки: перезагрузка той же
// карточки режим сохраняет.

const KEY = 'skriptes:edit-mode';

function currentPath(): string {
  return typeof window === 'undefined' ? '' : window.location.pathname;
}

function read(): string | null {
  try {
    const v = sessionStorage.getItem(KEY);
    // '1' — значение до 1.56.1 (режим на всю вкладку): не переносим.
    return v && v !== '1' ? v : null;
  } catch {
    return null;
  }
}

let scope = read();
const listeners = new Set<() => void>();

/** setEditMode — включить (на текущей карточке) / выключить режим правки. */
export function setEditMode(on: boolean) {
  scope = on ? currentPath() : null;
  try {
    if (scope) sessionStorage.setItem(KEY, scope);
    else sessionStorage.removeItem(KEY);
  } catch {
    // приватный режим — флаг живёт до перезагрузки
  }
  listeners.forEach((l) => l());
}

/** syncEditModeWithPath — Layout зовёт на каждую смену пути: режим включён у
 *  другой карточки — выключить (иначе он вернулся бы вместе с карточкой). */
export function syncEditModeWithPath(pathname: string) {
  if (scope !== null && scope !== pathname) setEditMode(false);
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
}

/** useEditMode — включён ли режим правки на этой карточке (без проверки роли).
 *  Путь сверяется и при рендере: новая карточка рисуется раньше эффекта Layout —
 *  без мигания контролов правки. */
export function useEditMode(): boolean {
  const s = useSyncExternalStore(
    subscribe,
    () => scope,
    () => null,
  );
  return s !== null && s === currentPath();
}

/** useIsAdmin — пользователь — админ. */
export function useIsAdmin(): boolean {
  return useMe().data?.role === 'admin';
}

/** useCanEdit — показывать контролы правки: админ и режим правки включён. */
export function useCanEdit(): boolean {
  const admin = useIsAdmin();
  const on = useEditMode();
  return admin && on;
}
