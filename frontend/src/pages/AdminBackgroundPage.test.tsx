import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AdminBackgroundPage } from './AdminBackgroundPage';

// AdminTabs использует router Link — вне RouterProvider он падает; мокаем.
vi.mock('@/components/AdminTabs', () => ({ AdminTabs: () => null }));

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{ui}</QueryClientProvider>;
}

const collection = {
  cache_max_mb: 8192,
  cache_min_free_mb: 1024,
  prewarm: false,
  sync_covers: true,
  sync_annotations: true,
  sync_years: true,
  intensity: 'medium',
  prewarm_running: false,
  prewarm_mode: 'off',
  cache_size_bytes: 1048576, // 1 МБ
  free_bytes: 5368709120, // 5 ГБ
};

const yearEnrichment = {
  enabled: false,
  openlibrary: true,
  wikidata: true,
  whole_collection: false,
  openlibrary_rpm: 60,
  wikidata_rpm: 20,
  not_found_retry_days: 90,
  error_retry_hours: 24,
  year_backfill_running: false,
  year_backfill_mode: 'off',
  coverage: { total: 0, with_year: 0, by_source: {} },
};

const coverEnrichment = {
  enabled: false,
  openlibrary: true,
  googlebooks: true,
  whole_collection: false,
  openlibrary_rpm: 60,
  googlebooks_rpm: 60,
  not_found_retry_days: 90,
  error_retry_hours: 24,
  cover_backfill_running: false,
  cover_backfill_mode: 'off',
  coverage: { total: 10, with_cover: 7, by_source: { openlibrary: 2 } },
};

let collectionPut: unknown = null;
let coverPut: unknown = null;

beforeEach(() => {
  collectionPut = null;
  coverPut = null;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string | Request, init?: RequestInit) => {
      const u = typeof url === 'string' ? url : url.url;
      const json = (body: unknown) =>
        new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } });
      if (u.includes('/api/admin/cover-enrichment')) {
        if (init?.method === 'PUT') {
          coverPut = JSON.parse(String(init.body));
          return json({ ...coverEnrichment, ...(coverPut as object) });
        }
        return json(coverEnrichment);
      }
      if (u.includes('/api/admin/year-enrichment')) {
        return json(yearEnrichment);
      }
      if (u.includes('/api/admin/cover-cache')) {
        if (init?.method === 'PUT') {
          collectionPut = JSON.parse(String(init.body));
          return json({ ...collection, ...(collectionPut as object) });
        }
        return json(collection);
      }
      return new Response('not mocked', { status: 404 });
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

describe('AdminBackgroundPage', () => {
  it('заполняет лимиты и статистику из настроек', async () => {
    render(wrap(<AdminBackgroundPage />));
    const max = (await screen.findByLabelText('Бюджет кэша, МБ')) as HTMLInputElement;
    expect(max.value).toBe('8192');
    const minFree = screen.getByLabelText('Порог свободного места, МБ') as HTMLInputElement;
    expect(minFree.value).toBe('1024');
    expect(screen.getByText('1.0 МБ')).toBeInTheDocument();
    expect(screen.getByText('5.0 ГБ')).toBeInTheDocument();
  });

  it('предупреждает при пороге свободного места < 100 МБ', async () => {
    const user = userEvent.setup();
    render(wrap(<AdminBackgroundPage />));
    const minFree = await screen.findByLabelText('Порог свободного места, МБ');
    await user.clear(minFree);
    await user.type(minFree, '50');
    expect(screen.getByText(/Безопаснее держать/)).toBeInTheDocument();
  });

  it('сохранение лимитов шлёт PUT с полным конфигом обработки коллекции', async () => {
    const user = userEvent.setup();
    render(wrap(<AdminBackgroundPage />));
    const max = await screen.findByLabelText('Бюджет кэша, МБ');
    await user.clear(max);
    await user.type(max, '4096');
    await user.click(screen.getByRole('button', { name: 'Сохранить' }));
    await vi.waitFor(() => {
      expect(collectionPut).toEqual({
        cache_max_mb: 4096,
        cache_min_free_mb: 1024,
        prewarm: false,
        sync_covers: true,
        sync_annotations: true,
        sync_years: true,
        intensity: 'medium',
      });
    });
  });

  it('показывает карточку обложек с покрытием и сохраняет её rpm', async () => {
    const user = userEvent.setup();
    render(wrap(<AdminBackgroundPage />));
    // Покрытие обложек из coverage-мока: 7 из 10 (70%).
    expect(await screen.findByText('7 из 10 (70%)')).toBeInTheDocument();
    const gbRpm = screen.getByLabelText('Google Books, запросов/мин') as HTMLInputElement;
    expect(gbRpm.value).toBe('60');
    await user.clear(gbRpm);
    await user.type(gbRpm, '30');
    await user.click(screen.getByRole('button', { name: 'Сохранить' }));
    await vi.waitFor(() => {
      expect(coverPut).toEqual({
        enabled: false,
        openlibrary: true,
        googlebooks: true,
        whole_collection: false,
        openlibrary_rpm: 60,
        googlebooks_rpm: 30,
        not_found_retry_days: 90,
        error_retry_hours: 24,
      });
    });
  });

  it('включение режима «вся коллекция» для обложек шлёт whole_collection=true', async () => {
    const user = userEvent.setup();
    render(wrap(<AdminBackgroundPage />));
    await screen.findByText('7 из 10 (70%)');
    // Два свитча «Вся коллекция»: [0] — годы, [1] — обложки.
    const switches = screen.getAllByLabelText('Вся коллекция (иначе только где fb2 не дал)');
    expect(switches.length).toBe(2);
    await user.click(switches[1]);
    await vi.waitFor(() => {
      expect((coverPut as { whole_collection?: boolean })?.whole_collection).toBe(true);
    });
  });
});
