import { describe, it, expect } from 'vitest';
import { genreChipClass } from './appearance';

describe('genreChipClass', () => {
  it('soft → приглушённый класс (bg-muted, muted-текст, мельче)', () => {
    const c = genreChipClass('soft');
    expect(c).toContain('bg-muted');
    expect(c).toContain('text-muted-foreground');
    expect(c).toContain('text-[11px]');
  });

  it('classic → стандартная secondary-плашка (без bg-muted, text-xs)', () => {
    const c = genreChipClass('classic');
    expect(c).not.toContain('bg-muted');
    expect(c).toContain('text-xs');
  });
});
