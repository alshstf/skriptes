import { describe, it, expect } from 'vitest';
import { collapseGenreChips } from '@/lib/genres';
import type { GenreItem } from '@/lib/genres';

const g = (code: string, category: string): GenreItem => ({
  id: 0,
  code,
  display: code,
  book_count: 0,
  category_name: category,
});

// Справочник: «Фантастика» (3 leaf'а), «Детективы» (2), одиночная «Поэзия».
const ALL = [
  g('sf', 'Фантастика'),
  g('sf_action', 'Фантастика'),
  g('sf_space', 'Фантастика'),
  g('det_classic', 'Детективы'),
  g('det_irony', 'Детективы'),
  g('poetry', 'Поэзия'),
];

describe('collapseGenreChips', () => {
  it('полная категория схлопывается в один чип, остальное — в rest', () => {
    const { fullCategories, rest } = collapseGenreChips(
      ['sf', 'sf_action', 'sf_space', 'det_classic'],
      ALL,
    );
    expect(fullCategories).toEqual([
      { name: 'Фантастика', codes: ['sf', 'sf_action', 'sf_space'] },
    ]);
    expect(rest).toEqual(['det_classic']);
  });

  it('неполная категория не схлопывается', () => {
    const { fullCategories, rest } = collapseGenreChips(['sf', 'sf_action'], ALL);
    expect(fullCategories).toEqual([]);
    expect(rest).toEqual(['sf', 'sf_action']);
  });

  it('одиночный жанр категории-одиночки остаётся обычным чипом', () => {
    const { fullCategories, rest } = collapseGenreChips(['poetry'], ALL);
    expect(fullCategories).toEqual([]);
    expect(rest).toEqual(['poetry']);
  });

  it('несколько полных категорий одновременно', () => {
    const { fullCategories, rest } = collapseGenreChips(
      ['det_irony', 'det_classic', 'sf', 'sf_action', 'sf_space'],
      ALL,
    );
    expect(fullCategories.map((c) => c.name)).toEqual(['Фантастика', 'Детективы']);
    expect(rest).toEqual([]);
  });

  it('пустой справочник (запрос в полёте) → всё в rest', () => {
    const { fullCategories, rest } = collapseGenreChips(['sf', 'sf_action'], []);
    expect(fullCategories).toEqual([]);
    expect(rest).toEqual(['sf', 'sf_action']);
  });
});
