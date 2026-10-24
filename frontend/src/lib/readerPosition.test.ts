import { beforeEach, describe, expect, it } from 'vitest';
import {
  adoptServerPosition,
  clearLocalPosition,
  loadLocalPosition,
  markPositionSynced,
  rememberLocalPosition,
  startPosition,
} from './readerPosition';

describe('readerPosition', () => {
  beforeEach(() => window.localStorage.clear());

  it('без локальной копии — серверная позиция', () => {
    expect(startPosition(7, 'cfi-a')).toEqual({ cfi: 'cfi-a', fromLocal: false });
    expect(startPosition(7, '')).toEqual({ cfi: '', fromLocal: false });
  });

  it('не дошедшее до сервера чтение — с локальной копии', () => {
    adoptServerPosition(7, 'cfi-a');
    rememberLocalPosition(7, 'cfi-b', 0.2);
    markPositionSynced(7, 'cfi-b');
    rememberLocalPosition(7, 'cfi-c', 0.3); // PUT не ушёл: вкладку выгрузили
    expect(startPosition(7, 'cfi-b')).toEqual({ cfi: 'cfi-c', fromLocal: true });
  });

  it('первое открытие, ни одно сохранение не дошло — тоже локальная', () => {
    rememberLocalPosition(7, 'cfi-b', 0.2);
    expect(startPosition(7, '')).toEqual({ cfi: 'cfi-b', fromLocal: true });
  });

  it('сервер сменился без нас (другое устройство) — прав сервер', () => {
    adoptServerPosition(7, 'cfi-a');
    rememberLocalPosition(7, 'cfi-b', 0.2);
    expect(startPosition(7, 'cfi-z')).toEqual({ cfi: 'cfi-z', fromLocal: false });
  });

  it('подтверждение сервера не трогает более свежее место', () => {
    rememberLocalPosition(7, 'cfi-b', 0.2);
    rememberLocalPosition(7, 'cfi-c', 0.3);
    markPositionSynced(7, 'cfi-b'); // ответ на PUT cfi-b пришёл после листания
    expect(loadLocalPosition(7)).toEqual({ cfi: 'cfi-c', fraction: 0.3, synced: 'cfi-b' });
  });

  it('у каждой книги своя копия; сброс — после дочитывания', () => {
    rememberLocalPosition(7, 'cfi-b', 0.2);
    rememberLocalPosition(8, 'cfi-x', 0.5);
    clearLocalPosition(7);
    expect(loadLocalPosition(7)).toBeNull();
    expect(loadLocalPosition(8)?.cfi).toBe('cfi-x');
  });

  it('битое значение в хранилище — как его нет', () => {
    window.localStorage.setItem('skriptes.reader.pos.7', '{oops');
    expect(loadLocalPosition(7)).toBeNull();
    expect(startPosition(7, 'cfi-a').cfi).toBe('cfi-a');
  });
});
