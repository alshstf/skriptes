import { describe, it, expect } from 'vitest';
import { READER_DEFAULTS, READER_THEMES, frameSettings, normalizeReaderSettings } from './readerSettings';

describe('normalizeReaderSettings', () => {
  it('мусор и пустое → значения по умолчанию', () => {
    expect(normalizeReaderSettings(null)).toEqual(READER_DEFAULTS);
    expect(normalizeReaderSettings({ theme: 'neon', margins: 'huge', fontSize: 'big', swipe: 'yes' })).toEqual(
      READER_DEFAULTS,
    );
  });

  it('размер шрифта — в пределах 70…200 %', () => {
    expect(normalizeReaderSettings({ fontSize: 10 }).fontSize).toBe(70);
    expect(normalizeReaderSettings({ fontSize: 500 }).fontSize).toBe(200);
    expect(normalizeReaderSettings({ fontSize: 120 }).fontSize).toBe(120);
  });

  it('свайп и тапы сразу выключить нельзя — тапы остаются', () => {
    const s = normalizeReaderSettings({ swipe: false, tapZones: false });
    expect(s).toMatchObject({ swipe: false, tapZones: true });
  });
});

describe('frameSettings', () => {
  it('«Авто» — по системной теме устройства', () => {
    const s = { ...READER_DEFAULTS, theme: 'auto' as const };
    expect(frameSettings(s, true)).toMatchObject({ bg: READER_THEMES.dark.bg, dark: true });
    expect(frameSettings(s, false)).toMatchObject({ bg: READER_THEMES.light.bg, dark: false });
  });

  it('поля и способы листать — в пиксели и флаги iframe', () => {
    const f = frameSettings({ ...READER_DEFAULTS, margins: 'wide', swipe: false, animation: false }, false);
    expect(f).toMatchObject({ margin: 56, gap: 11, swipe: false, tap: true, animated: false });
  });
});
