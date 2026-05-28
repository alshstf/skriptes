import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { LanguageVisibilityList, GenreVisibilityList } from './ContentVisibility';
import type { GenreItem } from '@/lib/genres';
import type { LanguageItem } from '@/lib/content';

/**
 * Контрол «Скрыть» (кнопка role=checkbox, крест вместо галочки): aria-checked
 * = скрыто. locked (admin-скрытое) — отмечено + disabled (нельзя включить
 * обратно). Проверяем эти инварианты + что onChange меняет только собственный
 * hidden-набор.
 */

const langs: LanguageItem[] = [
  { code: 'ru', display: 'Русский', book_count: 5 },
  { code: 'bg', display: 'Болгарский', book_count: 2 },
];

describe('LanguageVisibilityList', () => {
  it('отмечает скрытые языки, переключение меняет набор', () => {
    const onChange = vi.fn();
    render(<LanguageVisibilityList languages={langs} hidden={['bg']} onChange={onChange} />);

    const ru = screen.getByLabelText('Скрыть язык: Русский');
    const bg = screen.getByLabelText('Скрыть язык: Болгарский');
    expect(ru.getAttribute('aria-checked')).toBe('false');
    expect(bg.getAttribute('aria-checked')).toBe('true');

    fireEvent.click(ru); // скрыть ru → добавляется к hidden
    expect(onChange).toHaveBeenCalledWith(['bg', 'ru']);

    fireEvent.click(bg); // показать bg → убирается из hidden
    expect(onChange).toHaveBeenLastCalledWith([]);
  });

  it('admin-скрытый язык отмечен и заблокирован', () => {
    const onChange = vi.fn();
    render(
      <LanguageVisibilityList languages={langs} hidden={[]} locked={['bg']} onChange={onChange} />,
    );
    const bg = screen.getByLabelText('Скрыть язык: Болгарский') as HTMLButtonElement;
    expect(bg.getAttribute('aria-checked')).toBe('true');
    expect(bg.disabled).toBe(true);
  });
});

describe('GenreVisibilityList', () => {
  it('admin-скрытый жанр отмечен и заблокирован', async () => {
    const onChange = vi.fn();
    const genres: GenreItem[] = [
      { id: 1, code: 'erotica', display: 'Эротика', book_count: 3, category_name: 'Прочее' },
    ];
    render(<GenreVisibilityList genres={genres} hidden={[]} locked={['erotica']} onChange={onChange} />);
    // Категория с locked-жанром авто-раскрывается → leaf виден.
    const cb = (await screen.findByLabelText('Скрыть жанр: Эротика')) as HTMLButtonElement;
    expect(cb.getAttribute('aria-checked')).toBe('true');
    expect(cb.disabled).toBe(true);

    // Категория целиком из locked-жанров → её переключатель тоже заблокирован.
    const cat = screen.getByLabelText('Категория «Прочее» скрыта администратором') as HTMLButtonElement;
    expect(cat.getAttribute('aria-checked')).toBe('true');
    expect(cat.disabled).toBe(true);
  });

  it('переключение собственного скрытого жанра убирает его из набора', async () => {
    const onChange = vi.fn();
    const genres: GenreItem[] = [
      { id: 2, code: 'detective', display: 'Детектив', book_count: 3, category_name: 'Детективы' },
    ];
    render(<GenreVisibilityList genres={genres} hidden={['detective']} onChange={onChange} />);
    const cb = (await screen.findByLabelText('Скрыть жанр: Детектив')) as HTMLButtonElement;
    expect(cb.getAttribute('aria-checked')).toBe('true');
    expect(cb.disabled).toBe(false);

    fireEvent.click(cb);
    expect(onChange).toHaveBeenCalledWith([]);
  });
});
