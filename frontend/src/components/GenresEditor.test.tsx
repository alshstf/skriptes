import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { GenresEditor } from './GenresEditor';

/**
 * Регресс: у книги без жанров бэкенд отдаёт `"genres": null`, и карточка
 * падала в белый экран на `genres.length` (поймано живым прогоном на книге,
 * созданной без жанров; на проде воспроизводится, если админ снял все).
 */
describe('GenresEditor', () => {
  const wrap = (ui: React.ReactElement) => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
  };

  it('не падает на genres=null', () => {
    expect(() => wrap(<GenresEditor workId={1} genres={null} />)).not.toThrow();
  });

  it('не падает на genres=undefined', () => {
    expect(() => wrap(<GenresEditor workId={1} genres={undefined} />)).not.toThrow();
  });
});
