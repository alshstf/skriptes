import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { BooksPage } from './BooksPage';

// TanStack Router компоненты нам не нужны для теста списка — мокаем Link
// в обычный <a href="..."> чтобы у элемента был role="link", и
// заменяем useSearch/useNavigate стабами (URL-стейтом мы тут не управляем,
// а возвращать пустой объект достаточно).
vi.mock('@tanstack/react-router', () => {
  type LinkProps = {
    to?: string;
    params?: Record<string, string>;
    children?: React.ReactNode;
    className?: string;
  };
  return {
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
    useSearch: () => ({}),
    useNavigate: () => () => undefined,
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
      vi.fn(async (url: string | Request) => {
        const u = typeof url === 'string' ? url : url.url;
        // GroupedGenresFilter (внутри FiltersSidebar) грузит /api/genres
        // отдельно — отдаём пустой список чтобы он render'ил null и не
        // мешал тестам про список книг.
        if (u.includes('/api/genres')) {
          return new Response(JSON.stringify({ items: [] }), {
            status: 200,
            headers: { 'content-type': 'application/json' },
          });
        }
        return new Response(JSON.stringify(fixture), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      }),
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

  it('chips жанров в карточке книги показывают display, а не fb2_code', async () => {
    // Override default mock: отдаём не пустой /api/genres, а словарь с
    // display'ями для двух кодов из фикстуры — sf_action и popadanec.
    // Цель — убедиться что BookCard'ы используют useGenreMap, а не
    // рисуют код. prose остаётся без перевода → должен fallback'нуться
    // на сам код.
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string | Request) => {
        const u = typeof url === 'string' ? url : url.url;
        if (u.includes('/api/genres')) {
          return new Response(
            JSON.stringify({
              items: [
                { id: 1, code: 'sf_action', display: 'Боевая фантастика', book_count: 4 },
                { id: 2, code: 'popadanec', display: 'Попаданцы', book_count: 2 },
              ],
            }),
            { status: 200, headers: { 'content-type': 'application/json' } },
          );
        }
        return new Response(JSON.stringify(fixture), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      }),
    );
    render(wrap(<BooksPage />));
    // Display'и должны появиться после загрузки /api/genres.
    expect(await screen.findByText('Боевая фантастика')).toBeInTheDocument();
    expect(screen.getByText('Попаданцы')).toBeInTheDocument();
    // Fallback: 'prose' нет в словаре — рисуем сырой код.
    expect(screen.getByText('prose')).toBeInTheDocument();
    // Сам код sf_action в DOM не должен появиться вообще (если бы остался
    // старый код, видели бы оба — и плашку «sf_action», и не видели бы
    // «Боевая фантастика»).
    expect(screen.queryByText('sf_action')).not.toBeInTheDocument();
  });

  it('карточки книг запрашивают обложку on-demand по id', async () => {
    render(wrap(<BooksPage />));
    // Список ходит за обложкой по id книги (on-demand извлечение на
    // бэке), без зависимости от cover_path. Монограм-fallback на 404
    // живёт в onError <img> — в jsdom не срабатывает (картинки не
    // грузятся), поэтому тут проверяем сам src; fallback покрыт в
    // BookCover.test.
    const img1 = await screen.findByRole('img', { name: 'Обложка: Кадетский корпус. Книга 2' });
    expect(img1).toHaveAttribute('src', '/api/covers/book/1');
    const img2 = screen.getByRole('img', { name: 'Обложка: Гост' });
    expect(img2).toHaveAttribute('src', '/api/covers/book/2');
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
