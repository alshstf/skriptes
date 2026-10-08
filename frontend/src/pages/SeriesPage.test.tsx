import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

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
    useParams: () => ({ id: '7' }),
    useRouter: () => ({
      history: { back: vi.fn() },
      navigate: vi.fn(),
    }),
    useCanGoBack: () => false,
  };
});

import { SeriesPage } from './SeriesPage';
import { setEditMode } from '@/lib/editMode';

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{ui}</QueryClientProvider>;
}

// Две книги с одним номером тома в разных работах — у обычной серии это
// повод подсказать объединение изданий.
const series = {
  id: 7,
  title: 'Мини-Шарм',
  book_count: 2,
  books: [
    { id: 19, work_id: 190, title: 'Любовь в Париже', authors: ['Иванова Анна'], ser_no: 3, lib_id: '1' },
    { id: 20, work_id: 200, title: 'Сердце на ладони', authors: ['Петрова Ольга'], ser_no: 3, lib_id: '2' },
  ],
};

function stubFetch(payload: object) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      const body = url.includes('/api/auth/me')
        ? { user: { id: 1, email: 'admin@skriptes.test', role: 'admin' } }
        : payload;
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    }),
  );
}

describe('SeriesPage', () => {
  beforeEach(() => vi.unstubAllGlobals());
  afterEach(() => {
    vi.unstubAllGlobals();
    setEditMode(false);
  });

  it('обычная серия: админ видит подсказку объединить совпавший том', async () => {
    stubFetch(series);
    render(wrap(<SeriesPage />));
    expect(await screen.findByText(/Похоже, том #3/)).toBeInTheDocument();
  });

  it('межавторская серия: подсказки нет — один номер у разных романов', async () => {
    stubFetch({ ...series, kind: 'multi' });
    setEditMode(true); // ручное объединение — в режиме правки (#444)
    render(wrap(<SeriesPage />));
    expect(await screen.findByText('Межавторская или издательская серия')).toBeInTheDocument();
    // Ручное объединение админу по-прежнему доступно — значит, useMe уже
    // ответил «админ», и подсказки нет не из-за незагруженной роли.
    expect(await screen.findByRole('button', { name: /Объединить издания/ })).toBeInTheDocument();
    expect(screen.queryByText(/Похоже, том/)).not.toBeInTheDocument();
  });
});
