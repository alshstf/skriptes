import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AdminCoverCachePage } from './AdminCoverCachePage';

// AdminTabs использует router Link — вне RouterProvider он падает; мокаем.
vi.mock('@/components/AdminTabs', () => ({ AdminTabs: () => null }));

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{ui}</QueryClientProvider>;
}

const settings = {
  cache_max_mb: 8192,
  cache_min_free_mb: 1024,
  prewarm: false,
  cache_size_bytes: 1048576, // 1 МБ
  free_bytes: 5368709120, // 5 ГБ
};

let putBody: unknown = null;

beforeEach(() => {
  putBody = null;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string | Request, init?: RequestInit) => {
      const u = typeof url === 'string' ? url : url.url;
      if (u.includes('/api/admin/cover-cache')) {
        if (init?.method === 'PUT') {
          putBody = JSON.parse(String(init.body));
          return new Response(JSON.stringify({ ...settings, ...(putBody as object) }), {
            status: 200,
            headers: { 'content-type': 'application/json' },
          });
        }
        return new Response(JSON.stringify(settings), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      }
      return new Response('not mocked', { status: 404 });
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

describe('AdminCoverCachePage', () => {
  it('заполняет форму и статистику из настроек', async () => {
    render(wrap(<AdminCoverCachePage />));
    const max = (await screen.findByLabelText('Бюджет кэша, МБ')) as HTMLInputElement;
    expect(max.value).toBe('8192');
    const minFree = screen.getByLabelText('Порог свободного места, МБ') as HTMLInputElement;
    expect(minFree.value).toBe('1024');
    // Статистика (1 МБ кэш, 5 ГБ свободно).
    expect(screen.getByText('1.0 МБ')).toBeInTheDocument();
    expect(screen.getByText('5.0 ГБ')).toBeInTheDocument();
  });

  it('предупреждает при пороге свободного места < 100 МБ', async () => {
    const user = userEvent.setup();
    render(wrap(<AdminCoverCachePage />));
    const minFree = await screen.findByLabelText('Порог свободного места, МБ');
    await user.clear(minFree);
    await user.type(minFree, '50');
    expect(screen.getByText(/Безопаснее держать/)).toBeInTheDocument();
  });

  it('сохранение шлёт PUT с введёнными значениями', async () => {
    const user = userEvent.setup();
    render(wrap(<AdminCoverCachePage />));
    const max = await screen.findByLabelText('Бюджет кэша, МБ');
    await user.clear(max);
    await user.type(max, '4096');
    await user.click(screen.getByRole('button', { name: 'Сохранить' }));
    await vi.waitFor(() => {
      expect(putBody).toEqual({ cache_max_mb: 4096, cache_min_free_mb: 1024, prewarm: false });
    });
  });
});
