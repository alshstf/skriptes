import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { CommandPalette } from './CommandPalette';

const navigateMock = vi.fn();

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigateMock,
}));

const fixture = {
  query: 'кад',
  books: [
    {
      id: 7,
      title: 'Кадетский корпус. Книга 2',
      authors: ['Алексеев Евгений Артёмович'],
      series: 'Петля [Алексеев]',
      genres: [],
      year: 2023,
      lang: 'ru',
      lib_id: '749080',
    },
  ],
  authors: [
    { id: 11, full_name: 'Кадет Иван', book_count: 4 },
  ],
  series: [
    { id: 22, title: 'Кадетство', author_name: 'Иванов И.', book_count: 12 },
  ],
};

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{ui}</QueryClientProvider>;
}

describe('CommandPalette', () => {
  beforeEach(() => {
    navigateMock.mockReset();
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

  it('opens on Cmd+K and shows hint while query < 2 chars', async () => {
    const user = userEvent.setup();
    render(wrap(<CommandPalette />));
    expect(screen.queryByPlaceholderText(/Поиск книг/)).not.toBeInTheDocument();

    await user.keyboard('{Meta>}k{/Meta}');
    expect(await screen.findByPlaceholderText(/Поиск книг/)).toBeInTheDocument();
    // Подсказка в теле палитры — без хвоста "чтобы найти" (он только в sr-only описании).
    expect(screen.getByText('Введите минимум 2 символа')).toBeInTheDocument();
  });

  it('renders three sections (books / authors / series) for a query', async () => {
    const user = userEvent.setup();
    render(wrap(<CommandPalette />));
    await user.click(screen.getByLabelText('Открыть поиск'));
    const input = await screen.findByPlaceholderText(/Поиск книг/);
    await user.type(input, 'кад');

    expect(await screen.findByText('Кадетский корпус. Книга 2')).toBeInTheDocument();
    expect(screen.getByText('Кадет Иван')).toBeInTheDocument();
    expect(screen.getByText('Кадетство')).toBeInTheDocument();
    expect(screen.getByText('Книги')).toBeInTheDocument();
    expect(screen.getByText('Авторы')).toBeInTheDocument();
    expect(screen.getByText('Серии')).toBeInTheDocument();
  });

  it('navigates to book detail on click and closes', async () => {
    const user = userEvent.setup();
    render(wrap(<CommandPalette />));
    await user.click(screen.getByLabelText('Открыть поиск'));
    const input = await screen.findByPlaceholderText(/Поиск книг/);
    await user.type(input, 'кад');

    const item = await screen.findByText('Кадетский корпус. Книга 2');
    await user.click(item);

    await waitFor(() => {
      // Палитра ведёт на карточку работы (/works/{work_id ?? id}); у фикстуры
      // work_id нет → /works/7.
      expect(navigateMock).toHaveBeenCalledWith({ to: '/works/7' });
    });
    await waitFor(() => {
      expect(screen.queryByPlaceholderText(/Поиск книг/)).not.toBeInTheDocument();
    });
  });
});
