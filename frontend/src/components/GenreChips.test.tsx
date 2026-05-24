import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { GenreChips } from './GenreChips';

/**
 * Юнит-тест порядка чипсов. jsdom не считает layout → влезают все
 * (см. guard avail<=0 в GenreChips), поэтому overflow/«+N» проверяется
 * Playwright'ом, а здесь — только логика переупорядочивания совпавших с
 * фильтром жанров в начало. /api/genres мокаем пустым → display=код.
 */

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{ui}</QueryClientProvider>;
}

beforeEach(() => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string | Request) => {
      const u = typeof url === 'string' ? url : url.url;
      if (u.includes('/api/genres')) {
        return new Response(JSON.stringify({ items: [] }), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      }
      return new Response('not mocked', { status: 404 });
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

describe('GenreChips', () => {
  it('без фильтра — жанры в исходном порядке', async () => {
    render(wrap(<GenreChips genres={['aaa', 'bbb', 'ccc']} />));
    const chips = await screen.findAllByText(/^(aaa|bbb|ccc)$/);
    expect(chips.map((c) => c.textContent)).toEqual(['aaa', 'bbb', 'ccc']);
  });

  it('с активным фильтром совпавший жанр уходит в начало', async () => {
    render(wrap(<GenreChips genres={['aaa', 'bbb', 'ccc']} highlight={['ccc']} />));
    const chips = await screen.findAllByText(/^(aaa|bbb|ccc)$/);
    // ccc совпал с фильтром → первый; остальные сохраняют относительный порядок.
    expect(chips.map((c) => c.textContent)).toEqual(['ccc', 'aaa', 'bbb']);
  });

  it('фильтр без совпадений не меняет порядок', async () => {
    render(wrap(<GenreChips genres={['aaa', 'bbb']} highlight={['zzz']} />));
    const chips = await screen.findAllByText(/^(aaa|bbb)$/);
    expect(chips.map((c) => c.textContent)).toEqual(['aaa', 'bbb']);
  });
});
