import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AuthorsPage } from './AuthorsPage';

// Link → <a> (role=link). useSearch/useNavigate — фильтры теперь живут в URL;
// в юните стабим пустой search (дефолтные фильтры) и no-op navigate.
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
        for (const [k, v] of Object.entries(params)) href = href.replace(`$${k}`, v);
      }
      return (
        <a href={href} className={className}>
          {children}
        </a>
      );
    },
    useSearch: () => ({}),
    useNavigate: () => () => {},
  };
});

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{ui}</QueryClientProvider>;
}

const authorsFixture = {
  items: [
    {
      id: 42,
      full_name: 'Кинг Стивен',
      photo_path: '',
      book_count: 3,
      is_favorite: true,
      favorited_books_count: 2,
      top_genres: [{ code: 'sf_horror', display: 'Ужасы', count: 5 }],
      languages: ['ru', 'en'],
      years_active: { from: 1974, to: 2009 },
      has_adaptations: true,
      external_rating: 5,
      external_rating_source: 'library',
      reader_rating: 3.5,
      reader_rating_count: 2,
    },
    {
      id: 7,
      full_name: 'Толстой Лев',
      book_count: 1,
      is_favorite: false,
      favorited_books_count: 0,
      top_genres: [{ code: 'prose_classic', display: 'Классическая проза', count: 1 }],
      languages: ['ru'],
      has_adaptations: false,
    },
  ],
  total: 2,
};

function stubFetch(authors = authorsFixture) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string | Request) => {
      const u = typeof url === 'string' ? url : url.url;
      const json = (body: unknown) =>
        new Response(JSON.stringify(body), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      if (u.startsWith('/api/authors')) return json(authors);
      if (u.startsWith('/api/genres')) return json({ items: [] });
      if (u.startsWith('/api/languages'))
        return json({ items: [{ code: 'ru', display: 'Русский', book_count: 100 }] });
      if (u.startsWith('/api/content/effective'))
        return json({ hidden_genres: [], hidden_languages: [] });
      return json({});
    }),
  );
}

describe('AuthorsPage', () => {
  beforeEach(() => stubFetch());
  afterEach(() => vi.unstubAllGlobals());

  it('рендерит строки авторов с агрегатами и ссылкой на карточку', async () => {
    render(wrap(<AuthorsPage />));

    expect(await screen.findByRole('heading', { level: 3, name: 'Кинг Стивен' })).toBeInTheDocument();
    // book_count + годы активности.
    expect(screen.getByText(/3 книги в каталоге/)).toBeInTheDocument();
    expect(screen.getByText(/1974–2009/)).toBeInTheDocument();
    // «N книг в избранном» только при favorited_books_count > 0.
    expect(screen.getByText(/2 книги в избранном/)).toBeInTheDocument();
    // Топ-жанр (display из ответа; useGenreMap пуст → fallback на g.display).
    expect(screen.getByText('Ужасы')).toBeInTheDocument();
    // Единый внешний рейтинг отрендерен с источником в a11y-label (текст
    // тултипа — тот же; сам Radix-тултип монтирует контент только по ховеру).
    expect(screen.getByLabelText('Внешний рейтинг 5 · библиотека')).toBeInTheDocument();
    // Оценка читателей (book_ratings) — отдельно от внешнего рейтинга.
    expect(screen.getByLabelText('Оценка читателей 3.5 (2)')).toBeInTheDocument();
    // Иконка подписки (колокольчик) — только у Кинга.
    expect(screen.getByLabelText('Подписан')).toBeInTheDocument();
    // Иконка экранизаций — только у Кинга.
    expect(screen.getByLabelText('Есть экранизации')).toBeInTheDocument();

    // Толстой: без «в избранном» (favorited_books_count=0) и без звезды.
    expect(screen.getByRole('heading', { level: 3, name: 'Толстой Лев' })).toBeInTheDocument();

    // Ссылка строки ведёт на /authors/{id}.
    const link = screen.getByRole('link', { name: /Кинг Стивен/ });
    expect(link.getAttribute('href')).toBe('/authors/42');

    // Счётчик авторов в шапке.
    expect(screen.getByText(/^2 автора$/)).toBeInTheDocument();
  });

  it('дефолтная сортировка — «Сначала известные», дефолт не активный фильтр', async () => {
    render(wrap(<AuthorsPage />));
    await screen.findByRole('heading', { level: 3, name: 'Кинг Стивен' });

    // Селект(ы) сортировки (десктоп-сайдбар; мобильный дровер не смонтирован).
    const select = screen.getAllByLabelText('Сортировка')[0] as HTMLSelectElement;
    expect(select.value).toBe('renown');
    const labels = Array.from(select.options).map((o) => o.textContent);
    expect(labels[0]).toBe('Сначала известные');
    expect(labels).toContain('По алфавиту');
    expect(labels).not.toContain('По имени');

    // Дефолтная сортировка НЕ считается активным фильтром (нет кнопки «Сбросить»).
    expect(screen.queryByRole('button', { name: 'Сбросить' })).not.toBeInTheDocument();
  });

  it('показывает пустой стейт callout-ом, если авторов нет', async () => {
    stubFetch({ items: [], total: 0 });
    render(wrap(<AuthorsPage />));
    expect(await screen.findByText(/не нашлось/i)).toBeInTheDocument();
  });
});
