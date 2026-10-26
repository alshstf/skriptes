import { describe, it, expect } from 'vitest';
import { READER_DEFAULTS, READER_FONTS, READER_THEMES, frameSettings, normalizeReaderSettings } from './readerSettings';

describe('normalizeReaderSettings', () => {
  it('мусор и пустое → значения по умолчанию', () => {
    expect(normalizeReaderSettings(null)).toEqual(READER_DEFAULTS);
    expect(normalizeReaderSettings({ theme: 'neon', font: 'comic', margins: 'huge', fontSize: 'big', swipe: 'yes' })).toEqual(
      READER_DEFAULTS,
    );
  });

  it('размер шрифта — в пределах 70…200 %', () => {
    expect(normalizeReaderSettings({ fontSize: 10 }).fontSize).toBe(70);
    expect(normalizeReaderSettings({ fontSize: 500 }).fontSize).toBe(200);
    expect(normalizeReaderSettings({ fontSize: 120 }).fontSize).toBe(120);
  });

  it('шрифт — из меню; прежние настройки без шрифта получают книжный по умолчанию', () => {
    expect(normalizeReaderSettings({ font: 'ptsans' }).font).toBe('ptsans');
    expect(normalizeReaderSettings({ font: 'book' }).font).toBe('book');
    expect(normalizeReaderSettings({ theme: 'sepia', fontSize: 120 }).font).toBe('literata');
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
    expect(f).toMatchObject({ margin: 32, gap: 11, swipe: false, tap: true, animated: false });
  });

  it('шрифт — в font-family текста книги; «Как в книге» — без переопределения', () => {
    expect(frameSettings({ ...READER_DEFAULTS, font: 'ptserif' }, false).font).toBe("'PT Serif', Georgia, serif");
    expect(frameSettings({ ...READER_DEFAULTS, font: 'book' }, false).font).toBe('');
  });

  it('семейства шрифтов — без символов, ломающих CSS (iframe их отбрасывает)', () => {
    for (const { family } of Object.values(READER_FONTS)) expect(family).toMatch(/^[\w\s,'-]*$/);
  });
});
