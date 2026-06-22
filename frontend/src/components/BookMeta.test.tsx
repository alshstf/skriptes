import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { BookMeta } from './BookMeta';
import type { BookListItem } from '@/lib/books';

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{ui}</QueryClientProvider>;
}

const base: BookListItem = { id: 1, title: 'T', authors: ['A'], lib_id: 'L1' };

describe('BookMeta', () => {
  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'content-type': 'application/json' } })),
    );
  });
  afterEach(() => vi.unstubAllGlobals());

  it('рендерит все сигналы плашки', () => {
    render(
      wrap(
        <BookMeta
          book={{
            ...base,
            year: 1986,
            external_rating: 4,
            external_rating_source: 'library',
            reader_rating: 3.5,
            reader_rating_count: 2,
            has_adaptations: true,
            reading_fraction: 0.5,
            is_favorite: true,
          }}
        />,
      ),
    );
    expect(screen.getByText('1986')).toBeInTheDocument();
    expect(screen.getByLabelText('Внешний рейтинг 4 · библиотека')).toBeInTheDocument();
    expect(screen.getByLabelText('Оценка читателей 3.5 (2)')).toBeInTheDocument();
    expect(screen.getByLabelText('Есть экранизации')).toBeInTheDocument();
    expect(screen.getByLabelText('Прогресс чтения 50%')).toBeInTheDocument();
    expect(screen.getByLabelText('В избранном')).toBeInTheDocument();
  });

  it('прочитано перебивает прогресс (✓, без %)', () => {
    render(wrap(<BookMeta book={{ ...base, is_read: true, reading_fraction: 0.5 }} />));
    expect(screen.getByLabelText('Прочитано')).toBeInTheDocument();
    expect(screen.queryByLabelText('Прогресс чтения 50%')).not.toBeInTheDocument();
  });

  it('ничего не рендерит без сигналов', () => {
    const { container } = render(wrap(<BookMeta book={base} />));
    expect(container).toBeEmptyDOMElement();
  });
});
