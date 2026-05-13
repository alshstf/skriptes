import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { FavoriteButton } from './FavoriteButton';

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{ui}</QueryClientProvider>;
}

describe('FavoriteButton', () => {
  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(JSON.stringify({ is_favorite: true }), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        }),
      ),
    );
  });
  afterEach(() => vi.unstubAllGlobals());

  it('book: "В избранное" → POST /api/books/{id}/favorite', async () => {
    const user = userEvent.setup();
    render(wrap(<FavoriteButton target="book" id={42} isFavorite={false} />));
    const btn = screen.getByRole('button', { name: 'Добавить книгу в избранное' });
    expect(btn).toHaveAttribute('aria-pressed', 'false');
    await user.click(btn);
    await waitFor(() => {
      const fetchMock = (globalThis as unknown as { fetch: ReturnType<typeof vi.fn> }).fetch;
      const last = fetchMock.mock.calls.at(-1)!;
      expect(last[0]).toBe('/api/books/42/favorite');
      expect(last[1]).toMatchObject({ method: 'POST' });
    });
  });

  it('book: "В избранном" → DELETE на клик', async () => {
    const user = userEvent.setup();
    render(wrap(<FavoriteButton target="book" id={7} isFavorite={true} />));
    const btn = screen.getByRole('button', { name: 'Убрать книгу из избранного' });
    expect(btn).toHaveAttribute('aria-pressed', 'true');
    await user.click(btn);
    await waitFor(() => {
      const fetchMock = (globalThis as unknown as { fetch: ReturnType<typeof vi.fn> }).fetch;
      const last = fetchMock.mock.calls.at(-1)!;
      expect(last[0]).toBe('/api/books/7/favorite');
      expect(last[1]).toMatchObject({ method: 'DELETE' });
    });
  });

  it('author: POST /api/authors/{id}/favorite, лейбл "Подписаться"', async () => {
    const user = userEvent.setup();
    render(wrap(<FavoriteButton target="author" id={11} isFavorite={false} />));
    const btn = screen.getByRole('button', { name: 'Подписаться на автора' });
    await user.click(btn);
    await waitFor(() => {
      const fetchMock = (globalThis as unknown as { fetch: ReturnType<typeof vi.fn> }).fetch;
      const last = fetchMock.mock.calls.at(-1)!;
      expect(last[0]).toBe('/api/authors/11/favorite');
      expect(last[1]).toMatchObject({ method: 'POST' });
    });
  });

  it('series: DELETE /api/series/{id}/favorite, лейбл "Отписаться"', async () => {
    const user = userEvent.setup();
    render(wrap(<FavoriteButton target="series" id={22} isFavorite={true} />));
    const btn = screen.getByRole('button', { name: 'Отписаться от серии' });
    await user.click(btn);
    await waitFor(() => {
      const fetchMock = (globalThis as unknown as { fetch: ReturnType<typeof vi.fn> }).fetch;
      const last = fetchMock.mock.calls.at(-1)!;
      expect(last[0]).toBe('/api/series/22/favorite');
      expect(last[1]).toMatchObject({ method: 'DELETE' });
    });
  });
});
