import { useSyncExternalStore } from 'react';
import { useMe } from './auth';

// Режим правки каталога (#444): контролы правки (поля, жанры, авторы, серия,
// пересборка и слияние изданий, «служебный автор») админ видит только когда
// включил «Править» на карточке — иначе карточка как у читателя. Флаг — на
// вкладку (sessionStorage), общий для всех карточек.

const KEY = 'skriptes:edit-mode';

function read(): boolean {
  try {
    return sessionStorage.getItem(KEY) === '1';
  } catch {
    return false;
  }
}

let enabled = read();
const listeners = new Set<() => void>();

/** setEditMode — включить/выключить режим правки (все карточки перерисуются). */
export function setEditMode(on: boolean) {
  enabled = on;
  try {
    if (on) sessionStorage.setItem(KEY, '1');
    else sessionStorage.removeItem(KEY);
  } catch {
    // приватный режим — флаг живёт до перезагрузки
  }
  listeners.forEach((l) => l());
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
}

/** useEditMode — включён ли режим правки (без проверки роли). */
export function useEditMode(): boolean {
  return useSyncExternalStore(
    subscribe,
    () => enabled,
    () => false,
  );
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
