import { describe, it, expect, vi, afterEach } from 'vitest';
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

type State = {
  collection: Record<string, unknown>;
  year: Record<string, unknown>;
  cover: Record<string, unknown>;
  rating: Record<string, unknown>;
  ba: Record<string, unknown>;
  gates: Record<string, unknown>;
};

function baseState(): State {
  return {
    collection: {
      cache_max_mb: 8192,
      cache_min_free_mb: 1024,
      prewarm: false,
      sync_covers: true,
      sync_annotations: true,
      sync_years: true,
      intensity: 'medium',
      poster_cache_max_mb: 0,
      photo_cache_max_mb: 0,
      prewarm_running: false,
      prewarm_mode: 'off',
      cache_size_bytes: 1048576, // 1 МБ
      poster_cache_size_bytes: 0,
      photo_cache_size_bytes: 0,
      free_bytes: 5368709120, // 5 ГБ
    },
    year: {
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
      coverage: { total: 100, with_year: 41, by_source: { fb2_title: 30, openlibrary: 11 } },
    },
    cover: {
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
    },
    rating: {
      enabled: false,
      googlebooks: true,
      openlibrary: true,
      whole_collection: false,
      googlebooks_rpm: 60,
      openlibrary_rpm: 60,
      not_found_retry_days: 90,
      error_retry_hours: 24,
      external_rating_running: false,
      external_rating_mode: 'off',
      coverage: { total: 10, with_rating: 6, with_web: 1, by_source: { googlebooks: 1 } },
    },
    ba: {
      bios: false,
      adaptations: false,
      bios_rpm: 30,
      adaptations_rpm: 20,
      bios_running: false,
      bios_mode: 'off',
      adaptations_running: false,
      adaptations_mode: 'off',
      bio_coverage: { total: 8, with_bio: 5, with_photo: 3 },
      adaptation_coverage: { total: 10, with_adaptations: 4 },
    },
    gates: {
      cover_disabled: false,
      annotation_disabled: false,
      author_disabled: false,
      adaptation_disabled: false,
    },
  };
}

type Puts = {
  collection?: Record<string, unknown>;
  year?: Record<string, unknown>;
  cover?: Record<string, unknown>;
  rating?: Record<string, unknown>;
  ba?: Record<string, unknown>;
  gates?: Record<string, unknown>;
};

let state: State;
let puts: Puts;

function setup(mutate?: (s: State) => void) {
  state = baseState();
  mutate?.(state);
  puts = {};
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string | Request, init?: RequestInit) => {
      const u = typeof url === 'string' ? url : url.url;
      const json = (body: unknown) =>
        new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } });
      const put = init?.method === 'PUT';
      const body = put ? (JSON.parse(String(init?.body)) as Record<string, unknown>) : null;
      if (u.includes('/api/admin/enrichment-gates')) {
        if (put && body) {
          puts.gates = body;
          state.gates = { ...state.gates, ...body };
        }
        return json(state.gates);
      }
      if (u.includes('/api/admin/bio-adaptation-enrichment')) {
        if (put && body) {
          puts.ba = body;
          state.ba = { ...state.ba, ...body };
        }
        return json(state.ba);
      }
      if (u.includes('/api/admin/cover-enrichment')) {
        if (put && body) {
          puts.cover = body;
          state.cover = { ...state.cover, ...body };
        }
        return json(state.cover);
      }
      if (u.includes('/api/admin/external-rating')) {
        if (put && body) {
          puts.rating = body;
          state.rating = { ...state.rating, ...body };
        }
        return json(state.rating);
      }
      if (u.includes('/api/admin/year-enrichment')) {
        if (put && body) {
          puts.year = body;
          state.year = { ...state.year, ...body };
        }
        return json(state.year);
      }
      if (u.includes('/api/admin/cover-cache')) {
        if (put && body) {
          puts.collection = body;
          state.collection = { ...state.collection, ...body };
        }
        return json(state.collection);
      }
      return new Response('not mocked', { status: 404 });
    }),
  );
  return render(wrap(<AdminBackgroundPage />));
}

afterEach(() => vi.unstubAllGlobals());

describe('AdminBackgroundPage (аккордеон по типам)', () => {
  it('выводит типы с производными режимами и покрытием', async () => {
    setup();
    // Год: bg выключен (enabled=false, prewarm=false) → режим Выкл.
    expect(await screen.findByTestId('year-mode-off')).toHaveAttribute('aria-pressed', 'true');
    // Обложки: фон выключен → Лениво.
    expect(screen.getByTestId('cover-mode-lazy')).toHaveAttribute('aria-pressed', 'true');
    // Покрытия в свёрнутых заголовках.
    expect(screen.getByText('обложка у 70%')).toBeInTheDocument();
    expect(screen.getByText('год у 41%')).toBeInTheDocument();
  });

  it('режим выводится как Фоном, когда включён фоновый источник', async () => {
    setup((s) => {
      s.cover.enabled = true; // внешний воркер обложек включён
      s.ba.bios = true; // фоновые биографии
    });
    expect(await screen.findByTestId('cover-mode-bg')).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByTestId('author-mode-bg')).toHaveAttribute('aria-pressed', 'true');
  });

  it('«Выкл» у обложек пишет gate + выключает фоновые источники', async () => {
    const user = userEvent.setup();
    setup();
    await user.click(await screen.findByTestId('cover-mode-off'));
    await vi.waitFor(() => {
      expect(puts.gates?.cover_disabled).toBe(true);
    });
    // Локальная обработка обложек выключена, внешний воркер выключен.
    expect((puts.collection as { sync_covers?: boolean })?.sync_covers).toBe(false);
    expect((puts.cover as { enabled?: boolean })?.enabled).toBe(false);
  });

  it('«Фоном» у года включает локальный синк и внешний воркер', async () => {
    const user = userEvent.setup();
    setup();
    await user.click(await screen.findByTestId('year-mode-bg'));
    await vi.waitFor(() => {
      expect((puts.collection as { sync_years?: boolean })?.sync_years).toBe(true);
    });
    expect((puts.collection as { prewarm?: boolean })?.prewarm).toBe(true);
    expect((puts.year as { enabled?: boolean })?.enabled).toBe(true);
    // У года gate не трогаем (нет lazy-оси).
    expect(puts.gates).toBeUndefined();
  });

  it('предупреждает при пороге свободного места < 100 МБ', async () => {
    const user = userEvent.setup();
    const { container } = setup();
    await screen.findByTestId('cover-mode-lazy');
    const minFree = container.querySelector('#min-free') as HTMLInputElement;
    await user.clear(minFree);
    await user.type(minFree, '50');
    expect(screen.getByText(/Безопаснее держать/)).toBeInTheDocument();
  });

  it('правка числового поля показывает SaveBar и шлёт полный CollectionInput', async () => {
    const user = userEvent.setup();
    const { container } = setup();
    await screen.findByTestId('cover-mode-lazy');
    const minFree = container.querySelector('#min-free') as HTMLInputElement;
    expect(minFree.value).toBe('1024');
    await user.clear(minFree);
    await user.type(minFree, '2048');
    await user.click(screen.getByRole('button', { name: 'Сохранить' }));
    await vi.waitFor(() => {
      expect((puts.collection as { cache_min_free_mb?: number })?.cache_min_free_mb).toBe(2048);
    });
  });

  it('в режиме Лениво у обложек нет тумблеров источников — только пояснение', async () => {
    setup(); // обложки по умолчанию Лениво
    await screen.findByTestId('cover-mode-lazy');
    expect(screen.queryByLabelText('fb2 (локально, без сети)')).not.toBeInTheDocument();
    expect(screen.getByText(/обложка извлекается из fb2 по запросу/)).toBeInTheDocument();
  });

  it('в режиме Фоном fb2 — живой тумблер источника обложек', async () => {
    const user = userEvent.setup();
    setup((s) => {
      s.cover.enabled = true; // обложки Фоном (через внешний воркер)
    });
    const fb2 = (await screen.findByLabelText('fb2 (локально, без сети)')) as HTMLElement;
    // prewarm=false → локальный fb2-синк выключен, но тумблер доступен.
    expect(fb2).not.toBeChecked();
    await user.click(fb2);
    await vi.waitFor(() => {
      expect((puts.collection as { sync_covers?: boolean })?.sync_covers).toBe(true);
    });
    expect((puts.collection as { prewarm?: boolean })?.prewarm).toBe(true);
  });

  it('в режиме Фоном можно выключить внешний провайдер обложек (enabled пересчитывается)', async () => {
    const user = userEvent.setup();
    setup((s) => {
      s.cover.enabled = true;
      s.cover.googlebooks = false; // оставим только OpenLibrary
    });
    const ol = (await screen.findByLabelText('OpenLibrary')) as HTMLElement;
    await user.click(ol); // выключаем единственный внешний → enabled=false
    await vi.waitFor(() => {
      expect((puts.cover as { openlibrary?: boolean })?.openlibrary).toBe(false);
    });
    expect((puts.cover as { enabled?: boolean })?.enabled).toBe(false);
  });

  it('в режиме Фоном у обложек доступны внешние rpm и «Что заполнять»', async () => {
    const user = userEvent.setup();
    const { container } = setup((s) => {
      s.cover.enabled = true; // обложки в режиме Фоном
    });
    await screen.findByTestId('cover-mode-bg');
    const olRpm = container.querySelector('#cover-ol-rpm') as HTMLInputElement;
    expect(olRpm.value).toBe('60');
    // «Что заполнять» → «Всю коллекцию» применяется сразу.
    await user.click(screen.getAllByRole('button', { name: 'Всю коллекцию' })[0]);
    await vi.waitFor(() => {
      expect((puts.cover as { whole_collection?: boolean })?.whole_collection).toBe(true);
    });
  });

  it('внешний рейтинг: по умолчанию Выкл, покрытие в заголовке, «Фоном» включает воркер', async () => {
    const user = userEvent.setup();
    setup();
    expect(await screen.findByTestId('rating-mode-off')).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByText('рейтинг у 60%')).toBeInTheDocument();
    await user.click(screen.getByTestId('rating-mode-bg'));
    await vi.waitFor(() => {
      expect((puts.rating as { enabled?: boolean })?.enabled).toBe(true);
    });
  });

  it('внешний рейтинг в Фоном: выключение единственного источника гасит воркер (enabled пересчитывается)', async () => {
    const user = userEvent.setup();
    setup((s) => {
      s.rating.enabled = true;
      s.rating.openlibrary = false; // оставим только Google Books
    });
    const gb = (await screen.findByLabelText('Google Books (averageRating)')) as HTMLElement;
    await user.click(gb); // выключаем единственный внешний → enabled=false
    await vi.waitFor(() => {
      expect((puts.rating as { googlebooks?: boolean })?.googlebooks).toBe(false);
    });
    expect((puts.rating as { enabled?: boolean })?.enabled).toBe(false);
  });
});
