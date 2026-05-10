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
  book_count: 1,
  books_total: 1,
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
      lib_id: '749080',
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
    expect(await screen.findByRole('heading', { level: 1, name: 'Алексеев Евгений Артёмович' })).toBeInTheDocument();
    expect(screen.getByText(/1 книга в каталоге/)).toBeInTheDocument();
    expect(screen.getByText(/sf_action · 1/)).toBeInTheDocument();
    // среди ссылок есть прямая на /series/7 (карточка "Серии").
    const seriesLinks = screen.getAllByRole('link').filter((l) => l.getAttribute('href') === '/series/7');
    expect(seriesLinks.length).toBeGreaterThan(0);
    // книга в списке
    expect(screen.getByText('Кадетский корпус. Книга 2')).toBeInTheDocument();
  });
});
