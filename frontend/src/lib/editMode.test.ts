import { describe, it, expect, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { setEditMode, syncEditModeWithPath, useEditMode } from './editMode';

// Режим «Править» — у карточки, где его включили (владелец 2026-10-19): ушли —
// выключен, вернулись — тоже выключен; перезагрузка той же карточки — сохранён.
describe('editMode', () => {
  beforeEach(() => {
    window.history.replaceState(null, '', '/');
    setEditMode(false);
  });

  it('включается на текущей карточке и выключается при уходе с неё', () => {
    window.history.replaceState(null, '', '/authors/5');
    const { result, rerender } = renderHook(() => useEditMode());
    act(() => setEditMode(true));
    expect(result.current).toBe(true);
    expect(sessionStorage.getItem('skriptes:edit-mode')).toBe('/authors/5');

    // Другая карточка рисуется до эффекта Layout — режим уже выключен.
    window.history.pushState(null, '', '/works/9');
    rerender();
    expect(result.current).toBe(false);

    act(() => syncEditModeWithPath('/works/9'));
    window.history.pushState(null, '', '/authors/5');
    rerender();
    expect(result.current).toBe(false); // вернулись — режим не вернулся
    expect(sessionStorage.getItem('skriptes:edit-mode')).toBeNull();
  });

  it('смена пути на ту же карточку режим не трогает', () => {
    window.history.replaceState(null, '', '/series/3');
    const { result } = renderHook(() => useEditMode());
    act(() => setEditMode(true));
    act(() => syncEditModeWithPath('/series/3'));
    expect(result.current).toBe(true);
  });
});
