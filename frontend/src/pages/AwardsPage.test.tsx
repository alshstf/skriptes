import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

vi.mock('@tanstack/react-router', async () => {
  const actual = await vi.importActual<typeof import('@tanstack/react-router')>('@tanstack/react-router');
  type LinkProps = {
    to?: string;
    params?: Record<string, string>;
    hash?: string;
    children?: React.ReactNode;
    className?: string;
  };
  return {
    ...actual,
    Link: ({ to, params, hash, children, className }: LinkProps) => {
      let href = to ?? '#';
      if (params) {
        for (const [k, v] of Object.entries(params)) href = href.replace(`$${k}`, v);
      }
      return (
        <a href={hash ? `${href}#${hash}` : href} className={className}>
          {children}
        </a>
      );
    },
    useParams: () => ({ key: 'hugo' }),
  };
});

import { AwardPage, AwardsPage } from './AwardsPage';
import { groupByYear, type AwardWin } from '@/lib/awards';

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{ui}</QueryClientProvider>;
}

function stubFetch(byPath: Record<string, object>) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const path = new URL(String(input), 'http://x').pathname;
      return new Response(JSON.stringify(byPath[path] ?? {}), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    }),
  );
}

afterEach(() => vi.unstubAllGlobals());

const wins: AwardWin[] = [
  { id: 1, year: 1966, nomination: 'Роман', kind: 'work', title: 'Дюна', author: 'Фрэнк Герберт', work_id: 10,
    item: { id: 10, title: 'Дюна', authors: ['Херберт Фрэнк'], lib_id: '1' } },
  { id: 2, year: 1966, nomination: 'Рассказ', kind: 'work', title: 'Нет в библиотеке', author: 'Кто-то',
    source_url: 'https://fantlab.ru/work5' },
  { id: 3, year: 1959, nomination: 'Роман', kind: 'work', title: 'Тоже нет', author: 'Другой' },
];

describe('groupByYear', () => {
  it('годы в порядке ответа, внутри — номинации', () => {
    const g = groupByYear(wins);
    expect(g.map((y) => y.year)).toEqual([1966, 1959]);
    expect(g[0].noms.map((n) => n.nomination)).toEqual(['Роман', 'Рассказ']);
  });
});

describe('AwardsPage', () => {
  it('премии по разделам со счётчиком «в библиотеке»', async () => {
    stubFetch({
      '/api/awards': {
        items: [
          { key: 'nos', name: 'НОС', group: 'Русская литература', wins: 32, in_catalog: 28, first_year: 2009, last_year: 2021 },
          { key: 'hugo', name: 'Хьюго', group: 'Международные', wins: 0, in_catalog: 0 },
        ],
      },
    });
    render(wrap(<AwardsPage />));
    expect(await screen.findByText('НОС')).toBeTruthy();
    expect(screen.getByText('Русская литература')).toBeTruthy();
    expect(screen.getByText(/2009–2021 · в библиотеке 28 из 32/)).toBeTruthy();
    expect(screen.getByText('нет данных')).toBeTruthy();
    expect(screen.getByText('НОС').closest('a')?.getAttribute('href')).toBe('/awards/nos');
  });
});

describe('AwardPage', () => {
  it('книга из библиотеки — карточкой, остальные — строкой; фильтр «только из библиотеки»', async () => {
    stubFetch({ '/api/awards/hugo': { award: { key: 'hugo', name: 'Хьюго', group: 'Международные' }, wins } });
    render(wrap(<AwardPage />));
    expect(await screen.findByRole('heading', { name: 'Хьюго' })).toBeTruthy();
    expect(screen.getByText(/в библиотеке 1 из 3/)).toBeTruthy();
    expect(screen.getByRole('heading', { name: '1966' })).toBeTruthy();
    expect(screen.getByText('Нет в библиотеке')).toBeTruthy();
    expect(screen.getByText('Фантлаб').closest('a')?.getAttribute('href')).toBe('https://fantlab.ru/work5');

    fireEvent.click(screen.getByRole('switch', { name: 'Только книги из библиотеки' }));
    expect(screen.queryByText('Нет в библиотеке')).toBeNull();
    expect(screen.queryByRole('heading', { name: '1959' })).toBeNull();
    expect(screen.getByRole('heading', { name: '1966' })).toBeTruthy();
  });
});
