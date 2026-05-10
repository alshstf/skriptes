import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { BooksPage } from './BooksPage';

// TanStack Router компоненты нам не нужны для теста списка — мокаем Link
// в обычный <a href="..."> чтобы у элемента был role="link".
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
        for (const [k, v] of Object.entries(params)) {
          href = href.replace(`$${k}`, v);
        }
      }
      return (
        <a href={href} className={className}>
          {children}
        </a>
      );
    },
  };
});

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{ui}</QueryClientProvider>;
}

const fixture = {
  items: [
    {
      id: 1,
      title: 'Кадетский корпус. Книга 2',
      authors: ['Алексеев Евгений Артёмович'],
      series: 'Петля [Алексеев]',
      genres: ['sf_action', 'popadanec'],
      year: 2023,
      lang: 'ru',
      lib_id: '749080',
    },
    {
      id: 2,
      title: 'Гост',
      authors: ['Иванов Иван'],
      genres: ['prose'],
      lib_id: '25838',
    },
  ],
  total: 2,
  limit: 20,
  offset: 0,
  processing_ms: 5,
};

describe('BooksPage', () => {
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

  it('renders the list of books with authors and genres', async () => {
    render(wrap(<BooksPage />));
    expect(await screen.findByText('Кадетский корпус. Книга 2')).toBeInTheDocument();
    expect(screen.getByText('Алексеев Евгений Артёмович')).toBeInTheDocument();
    expect(screen.getByText(/Серия: Петля \[Алексеев\]/)).toBeInTheDocument();
    expect(screen.getAllByRole('link')).toHaveLength(2);
    // плюрализация: 2 книги
    expect(await screen.findByText(/2 книги/)).toBeInTheDocument();
  });

  it('debounces search input before sending the request', async () => {
    const user = userEvent.setup();
    render(wrap(<BooksPage />));
    const input = screen.getByPlaceholderText('Поиск по названию или автору');
    await user.type(input, 'кад');
    // После debounce (~200ms) должен прилететь fetch с q=кад
    await waitFor(() => {
      const fetchMock = (globalThis as unknown as { fetch: ReturnType<typeof vi.fn> }).fetch;
      const lastCall = fetchMock.mock.calls.at(-1);
      expect(lastCall?.[0]).toContain('q=%D0%BA%D0%B0%D0%B4'); // url-encoded "кад"
    });
  });
});
