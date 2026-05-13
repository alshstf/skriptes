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

  it('renders "В избранное" when not favorited and POSTs on click', async () => {
    const user = userEvent.setup();
    render(wrap(<FavoriteButton bookId={42} isFavorite={false} />));
    const btn = screen.getByRole('button', { name: 'Добавить в избранное' });
    expect(btn).toHaveAttribute('aria-pressed', 'false');
    await user.click(btn);
    await waitFor(() => {
      const fetchMock = (globalThis as unknown as { fetch: ReturnType<typeof vi.fn> }).fetch;
      const last = fetchMock.mock.calls.at(-1)!;
      expect(last[0]).toBe('/api/books/42/favorite');
      expect(last[1]).toMatchObject({ method: 'POST' });
    });
  });

  it('renders "В избранном" when favorited and DELETEs on click', async () => {
    const user = userEvent.setup();
    render(wrap(<FavoriteButton bookId={7} isFavorite={true} />));
    const btn = screen.getByRole('button', { name: 'Убрать из избранного' });
    expect(btn).toHaveAttribute('aria-pressed', 'true');
    await user.click(btn);
    await waitFor(() => {
      const fetchMock = (globalThis as unknown as { fetch: ReturnType<typeof vi.fn> }).fetch;
      const last = fetchMock.mock.calls.at(-1)!;
      expect(last[0]).toBe('/api/books/7/favorite');
      expect(last[1]).toMatchObject({ method: 'DELETE' });
    });
  });
});
