import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AuthorPage } from './AuthorPage';

vi.mock('@tanstack/react-router', async () => {
  const actual = await vi.importActual<typeof import('@tanstack/react-router')>('@tanstack/react-router');
  type LinkProps = {
    to?: string;
    params?: Record<string, string>;
    children?: React.ReactNode;
    className?: string;
  };
  return {
    ...actual,
    Link: ({ to, params, children, className }: LinkProps) => {
      let href = to ?? '#';
      if (params) {
        for (const [k, v] of Object.entries(params)) href = href.replace(`$${k}`, v);
      }
      return (
        <a href={href} className={className}>
          {children}
        </a>
      );
    },
    useParams: () => ({ id: '42' }),
    // Минимальные заглушки для BackButton — он использует useRouter и
    // useCanGoBack. В реальном приложении они работают через
    // RouterProvider, но изолированный тест компонента поднимать его не
    // должен.
    useRouter: () => ({
      history: { back: vi.fn() },
      navigate: vi.fn(),
    }),
    useCanGoBack: () => false,
  };
});

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{ui}</QueryClientProvider>;
}

const fixture = {
  id: 42,
  last_name: 'Алексеев',
  first_name: 'Евгений',
  middle_name: 'Артёмович',
  full_name: 'Алексеев Евгений Артёмович',
  book_count: 2,
  books_total: 2,
  top_genres: [
    { code: 'sf_action', display: 'sf_action', count: 1 },
    { code: 'popadanec', display: 'popadanec', count: 1 },
  ],
  series: [{ id: 7, title: 'Петля [Алексеев]', count: 1 }],
  books: [
    {
      id: 19,
      title: 'Кадетский корпус. Книга 2',
      authors: ['Алексеев Евгений Артёмович'],
      series: 'Петля [Алексеев]',
      series_id: 7,
      ser_no: 2,
      lib_id: '749080',
    },
    // Книга вне серий — должна оказаться в секции "Вне серий".
    {
      id: 20,
      title: 'Записки одиночки',
      authors: ['Алексеев Евгений Артёмович'],
      lib_id: '749081',
    },
  ],
};

describe('AuthorPage', () => {
  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(JSON.stringify(fixture), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        }),
      ),
    );
  });
  afterEach(() => vi.unstubAllGlobals());

  it('renders author header, top genres, series link and books', async () => {
    render(wrap(<AuthorPage />));
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Алексеев Евгений Артёмович' }),
    ).toBeInTheDocument();
    expect(screen.getByText(/2 книги в каталоге/)).toBeInTheDocument();
    expect(screen.getByText(/sf_action · 1/)).toBeInTheDocument();
    // Среди ссылок есть прямая на /series/7 — заголовок-секции серии.
    const seriesLinks = screen
      .getAllByRole('link')
      .filter((l) => l.getAttribute('href') === '/series/7');
    expect(seriesLinks.length).toBeGreaterThan(0);
    // Книги в обеих секциях.
    expect(screen.getByText('Кадетский корпус. Книга 2')).toBeInTheDocument();
    expect(screen.getByText('Записки одиночки')).toBeInTheDocument();
    // Псевдосекция для книг вне серий.
    expect(screen.getByText('Вне серий')).toBeInTheDocument();
  });

  it('сортирует книги серии по series_order, а не по порядку массива', async () => {
    const reordered = {
      ...fixture,
      series: [{ id: 7, title: 'Без номеров', count: 2 }],
      book_count: 2,
      books_total: 2,
      books: [
        // В массиве «Второй» идёт первым, но series_order у него больше →
        // в DOM он должен оказаться ПОСЛЕ «Первого».
        { id: 51, title: 'Второй том', authors: ['A'], series: 'Без номеров', series_id: 7, series_order: 1, lib_id: '51' },
        { id: 50, title: 'Первый том', authors: ['A'], series: 'Без номеров', series_id: 7, series_order: 0, lib_id: '50' },
      ],
    };
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(JSON.stringify(reordered), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        }),
      ),
    );
    render(wrap(<AuthorPage />));
    const first = await screen.findByText('Первый том');
    const second = screen.getByText('Второй том');
    // Первый том должен предшествовать Второму в DOM (series_order 0 < 1).
    expect(first.compareDocumentPosition(second) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('при enrichment_fetched и пустой bio сразу показывает fallback, а не скелетон', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(JSON.stringify({ ...fixture, enrichment_fetched: true }), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        }),
      ),
    );
    render(wrap(<AuthorPage />));
    expect(await screen.findByText('Информация отсутствует.')).toBeInTheDocument();
    // Скелетон биографии не висит — попытка обогащения уже была.
    expect(screen.queryByLabelText('Биография загружается')).not.toBeInTheDocument();
  });
});
