import { createContext, useContext } from 'react';

/**
 * Координация видимости поиска в хэдере с hero-поиском на Главной.
 *
 * На Главной крупный hero-инпут — доминанта; пока он на экране, дублировать
 * его кнопкой поиска в хэдере не нужно. HomePage через IntersectionObserver
 * сообщает, виден ли hero (`setHeroSearchVisible`), а Header прячет/показывает
 * свою кнопку поиска с быстрой анимацией. На остальных страницах hero нет —
 * значение по умолчанию false, кнопка в хэдере видна всегда.
 */
export type HeroSearchState = {
  heroSearchVisible: boolean;
  setHeroSearchVisible: (visible: boolean) => void;
};

export const HeroSearchContext = createContext<HeroSearchState>({
  heroSearchVisible: false,
  setHeroSearchVisible: () => {},
});

export function useHeroSearch() {
  return useContext(HeroSearchContext);
}
