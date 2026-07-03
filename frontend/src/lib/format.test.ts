import { describe, it, expect } from 'vitest';
import { shortPersonName, langGenitive, translationLine } from './format';

describe('shortPersonName', () => {
  it('сокращает ФИО до фамилии с инициалами', () => {
    expect(shortPersonName('Гинзбург Юлия Александровна')).toBe('Гинзбург Ю. А.');
    expect(shortPersonName('Вебер Виктор')).toBe('Вебер В.');
  });
  it('не трогает не-ФИО', () => {
    // Слэш/строчные слова — не имя.
    expect(shortPersonName('Любительский / сетевой перевод')).toBe(
      'Любительский / сетевой перевод',
    );
    // Односимвольный инициал в данных — уже сокращено, оставляем.
    expect(shortPersonName('Клименко Л')).toBe('Клименко Л');
    // Одно слово.
    expect(shortPersonName('Аноним')).toBe('Аноним');
  });
});

describe('langGenitive', () => {
  it('склоняет прилагательные на «-ий»', () => {
    expect(langGenitive('Французский')).toBe('французского');
    expect(langGenitive('Английский')).toBe('английского');
    expect(langGenitive('Немецкий')).toBe('немецкого');
  });
  it('не-прилагательные → null', () => {
    expect(langGenitive('Иврит')).toBeNull();
    expect(langGenitive('Хинди')).toBeNull();
    expect(langGenitive('Эсперанто')).toBeNull();
  });
});

describe('translationLine', () => {
  it('язык + переводчик → полная фраза титульного листа', () => {
    expect(translationLine('Французский', 'Гинзбург Юлия Александровна')).toBe(
      'Перевод с французского — Гинзбург Ю. А.',
    );
  });
  it('только язык', () => {
    expect(translationLine('Французский', null)).toBe('Перевод с французского');
  });
  it('только переводчик', () => {
    expect(translationLine(null, 'Вебер Виктор')).toBe('Перевод — Вебер В.');
  });
  it('несклоняемый язык — запасная формулировка', () => {
    expect(translationLine('Иврит', 'Вебер Виктор')).toBe('Перевод — Вебер В. (оригинал: иврит)');
    expect(translationLine('Иврит', null)).toBe('Оригинал: иврит');
  });
  it('ничего не известно → null', () => {
    expect(translationLine(null, null)).toBeNull();
    expect(translationLine('', '')).toBeNull();
  });
});
