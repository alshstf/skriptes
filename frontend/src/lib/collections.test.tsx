import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useMoveBookBetweenShelves } from './collections';

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

function wrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

describe('useMoveBookBetweenShelves', () => {
  let calls: { method: string; url: string }[];
  beforeEach(() => {
    calls = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string | Request, init?: RequestInit) => {
        calls.push({ method: init?.method ?? 'GET', url: typeof url === 'string' ? url : url.url });
        return new Response(null, { status: 204 });
      }),
    );
  });
  afterEach(() => vi.unstubAllGlobals());

  it('переносит книгу: POST в целевую, затем DELETE из исходной', async () => {
    const { result } = renderHook(() => useMoveBookBetweenShelves(), { wrapper: wrapper() });
    result.current.mutate({ bookId: 5, fromId: 1, toId: 2, toName: 'Избранное' });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(calls).toHaveLength(2);
    // add-first: сначала добавляем в целевую (2), потом убираем из исходной (1).
    expect(calls[0]).toEqual({ method: 'POST', url: '/api/me/collections/2/books/5' });
    expect(calls[1]).toEqual({ method: 'DELETE', url: '/api/me/collections/1/books/5' });
  });
});
