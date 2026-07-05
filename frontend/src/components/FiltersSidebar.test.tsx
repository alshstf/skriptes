import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { FiltersSidebar, type FiltersValue } from './FiltersSidebar';

/**
 * FiltersSidebar теперь содержит GroupedGenresFilter, который грузит
 * /api/genres через useQuery. Обёртка делает QueryClient + мокает fetch
 * через vi.stubGlobal, отдавая фикстуру из 4 жанров в 2 категориях
 * (Фантастика, Детективы) + один leaf без category для проверки группы
 * «Прочее».
 */

const emptyFilters: FiltersValue = {
  genres: [],
  lang: '',
  srcLang: '',
  yearFrom: 0,
  yearTo: 0,
  sort: '',
};

const genresFixture = {
  items: [
    {
      id: 1,
      code: 'sf_action',
      display: 'Боевая фантастика',
      book_count: 4,
      category_code: 'cat:sf',
      category_name: 'Фантастика',
    },
    {
      id: 2,
      code: 'sf_history',
      display: 'Альтернативная история',
      book_count: 3,
      category_code: 'cat:sf',
      category_name: 'Фантастика',
    },
    {
      id: 3,
      code: 'det_classic',
      display: 'Классический детектив',
      book_count: 5,
      category_code: 'cat:detective',
      category_name: 'Детективы и Триллеры',
    },
    {
      id: 4,
      code: 'misc_legacy',
      display: 'misc_legacy',
      book_count: 1,
      // no category — должен попасть в «Прочее»
    },
  ],
};

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{ui}</QueryClientProvider>;
}

beforeEach(() => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string | Request) => {
      const u = typeof url === 'string' ? url : url.url;
      if (u.includes('/api/genres')) {
        return new Response(JSON.stringify(genresFixture), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      }
      return new Response('not mocked', { status: 404 });
    }),
  );
});

describe('FiltersSidebar', () => {
  it('renders genre categories collapsed by default', async () => {
    render(
      wrap(
        <FiltersSidebar
          value={emptyFilters}
          onChange={() => {}}
          facets={{ genres: { sf_action: 4, sf_history: 3, det_classic: 5 }, lang: { ru: 12 } }}
          totalActive={0}
          onReset={() => {}}
        />,
      ),
    );
    // Категории должны появиться после загрузки списка.
    expect(await screen.findByText('Фантастика')).toBeInTheDocument();
    expect(screen.getByText('Детективы и Триллеры')).toBeInTheDocument();
    expect(screen.getByText('Прочее')).toBeInTheDocument(); // fallback для leaf без category
    // Сумма leaf-counts отображается рядом с категорией.
    expect(screen.getByText('7')).toBeInTheDocument(); // 4 (sf_action) + 3 (sf_history)
    expect(screen.getByText('5')).toBeInTheDocument(); // det_classic
    // Leaf'ы не видны пока секция свёрнута.
    expect(screen.queryByText('Боевая фантастика')).not.toBeInTheDocument();
  });

  it('clicking category expands it; leafs become visible', async () => {
    const user = userEvent.setup();
    render(
      wrap(
        <FiltersSidebar
          value={emptyFilters}
          onChange={() => {}}
          facets={{ genres: { sf_action: 4, sf_history: 3 } }}
          totalActive={0}
          onReset={() => {}}
        />,
      ),
    );
    await screen.findByText('Фантастика');
    // Scope chevron к row «Фантастика» — иначе getByRole('button',
    // {name:'Развернуть'}) находит сразу несколько (по одному на каждую
    // категорию).
    const fantasyRow = screen.getByText('Фантастика').closest('div')!;
    const chevron = fantasyRow.querySelector('button[aria-expanded="false"]') as HTMLButtonElement;
    expect(chevron).not.toBeNull();
    await user.click(chevron);
    // Теперь leaf'ы Фантастики видны
    expect(screen.getByText('Боевая фантастика')).toBeInTheDocument();
    expect(screen.getByText('Альтернативная история')).toBeInTheDocument();
  });

  it('tri-state: clicking category checkbox in none state selects all leafs', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      wrap(
        <FiltersSidebar
          value={emptyFilters}
          onChange={onChange}
          facets={{ genres: { sf_action: 4, sf_history: 3 } }}
          totalActive={0}
          onReset={() => {}}
        />,
      ),
    );
    await screen.findByText('Фантастика');
    // Tri-state checkbox у «Фантастика» — нативный input (state='none')
    // с aria-label 'Выбрать все в категории'. Их несколько (по одной
    // на каждую категорию); scope'имся к row «Фантастика».
    const fantasyRow = screen.getByText('Фантастика').closest('div')!;
    const selectAll = fantasyRow.querySelector('input[type="checkbox"]') as HTMLInputElement;
    expect(selectAll).not.toBeNull();
    await user.click(selectAll);
    expect(onChange).toHaveBeenCalledOnce();
    const call = onChange.mock.calls[0][0] as FiltersValue;
    // Должны выбраться все leaf'ы Фантастики (sf_action + sf_history)
    expect(call.genres).toEqual(expect.arrayContaining(['sf_action', 'sf_history']));
    expect(call.genres).toHaveLength(2);
  });

  it('tri-state: when all leafs selected, clicking checkbox deselects all', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      wrap(
        <FiltersSidebar
          value={{ ...emptyFilters, genres: ['sf_action', 'sf_history'] }}
          onChange={onChange}
          facets={{ genres: { sf_action: 4, sf_history: 3 } }}
          totalActive={2}
          onReset={() => {}}
        />,
      ),
    );
    await screen.findByText('Фантастика');
    const deselectAll = await screen.findByRole('checkbox', {
      name: /Снять выделение со всех/i,
    });
    await user.click(deselectAll);
    expect(onChange).toHaveBeenCalledOnce();
    const call = onChange.mock.calls[0][0] as FiltersValue;
    expect(call.genres).toEqual([]);
  });

  it('partial state shows mixed checkbox + "Свернуть" aria-label (auto-expanded)', async () => {
    render(
      wrap(
        <FiltersSidebar
          value={{ ...emptyFilters, genres: ['sf_action'] }}
          onChange={() => {}}
          facets={{ genres: { sf_action: 4, sf_history: 3 } }}
          totalActive={1}
          onReset={() => {}}
        />,
      ),
    );
    await screen.findByText('Фантастика');
    // Категория с selected leaf автоматически развёрнута.
    const fantasyRow = screen.getByText('Фантастика').closest('div')!;
    const chevron = fantasyRow.querySelector('button[aria-expanded="true"]');
    expect(chevron).not.toBeNull();
    // Mixed checkbox (partial state) имеет role=checkbox с aria-checked=mixed.
    const partial = screen.getByRole('checkbox', {
      name: /Выбрана часть жанров категории/i,
    });
    expect(partial).toHaveAttribute('aria-checked', 'mixed');
  });

  it('sorts categories by total count desc; «Прочее» always last', async () => {
    render(
      wrap(
        <FiltersSidebar
          value={emptyFilters}
          onChange={() => {}}
          // facets перевешивают book_count из словаря:
          //   sf_action 10 + sf_history 1 = Фантастика 11
          //   det_classic 3  = Детективы 3
          // → ожидаем порядок: Фантастика, Детективы, Прочее (фикстура misc_legacy без facets → 0)
          facets={{ genres: { sf_action: 10, sf_history: 1, det_classic: 3 } }}
          totalActive={0}
          onReset={() => {}}
        />,
      ),
    );
    await screen.findByText('Фантастика');
    // Все три заголовка в article — берём в порядке появления в DOM.
    const headers = ['Фантастика', 'Детективы и Триллеры', 'Прочее'];
    const positions = headers.map((h) => {
      const el = screen.getByText(h);
      return { h, top: el.getBoundingClientRect().top };
    });
    // jsdom не делает реальный layout, у всех top === 0 → сравниваем
    // через индекс в textContent родителя.
    const list = screen.getByText('Фантастика').closest('ul')!;
    const allTexts = Array.from(list.querySelectorAll('button.flex-1, button.text-left'))
      .map((b) => b.textContent?.trim())
      .filter((t): t is string => Boolean(t));
    const idxFantasy = allTexts.findIndex((t) => t === 'Фантастика');
    const idxDetective = allTexts.findIndex((t) => t === 'Детективы и Триллеры');
    const idxOther = allTexts.findIndex((t) => t === 'Прочее');
    expect(idxFantasy).toBeLessThan(idxDetective);
    expect(idxDetective).toBeLessThan(idxOther);
    // Просто чтобы убедиться что позиции используются (linter):
    expect(positions).toHaveLength(3);
  });

  it('reset button appears only when there are active filters', async () => {
    const user = userEvent.setup();
    const onReset = vi.fn();
    const { rerender } = render(
      wrap(
        <FiltersSidebar
          value={emptyFilters}
          onChange={() => {}}
          totalActive={0}
          onReset={onReset}
        />,
      ),
    );
    expect(screen.queryByRole('button', { name: /Сбросить/ })).not.toBeInTheDocument();

    rerender(
      wrap(
        <FiltersSidebar
          value={{ ...emptyFilters, genres: ['sf_action'] }}
          onChange={() => {}}
          totalActive={1}
          onReset={onReset}
        />,
      ),
    );
    const reset = screen.getByRole('button', { name: /Сбросить/ });
    await user.click(reset);
    expect(onReset).toHaveBeenCalled();
  });

  it('контекстный поиск по жанру фильтрует список и раскрывает совпавшее', async () => {
    const user = userEvent.setup();
    render(
      wrap(
        <FiltersSidebar
          value={emptyFilters}
          onChange={() => {}}
          totalActive={0}
          onReset={() => {}}
        />,
      ),
    );
    await screen.findByText('Фантастика');
    const search = screen.getByLabelText('Поиск жанра');
    await user.type(search, 'детектив');
    // «Детективы и Триллеры» совпадает по имени категории → видна и
    // раскрыта (leaf виден сразу).
    expect(screen.getByText('Детективы и Триллеры')).toBeInTheDocument();
    expect(screen.getByText('Классический детектив')).toBeInTheDocument();
    // Несовпавшие категории/жанры отфильтрованы.
    expect(screen.queryByText('Фантастика')).not.toBeInTheDocument();
    expect(screen.queryByText('Боевая фантастика')).not.toBeInTheDocument();
    expect(screen.queryByText('Прочее')).not.toBeInTheDocument();
  });

  it('поиск без совпадений показывает «Ничего не найдено»', async () => {
    const user = userEvent.setup();
    render(
      wrap(
        <FiltersSidebar
          value={emptyFilters}
          onChange={() => {}}
          totalActive={0}
          onReset={() => {}}
        />,
      ),
    );
    await screen.findByText('Фантастика');
    await user.type(screen.getByLabelText('Поиск жанра'), 'zzzнеттакого');
    expect(screen.getByText('Ничего не найдено')).toBeInTheDocument();
  });

  it('selecting a sort option triggers onChange', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      wrap(
        <FiltersSidebar
          value={emptyFilters}
          onChange={onChange}
          totalActive={0}
          onReset={() => {}}
        />,
      ),
    );
    await user.selectOptions(screen.getByLabelText('Сортировка'), 'year_desc');
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ sort: 'year_desc' }));
  });

  it('лейбл дефолтной сортировки контекстный; пункта «По популярности» нет', () => {
    // Без запроса дефолт = браузинг, а браузинг упорядочен популярностью
    // (popularity:desc — последний ranking rule) → честный лейбл.
    const { rerender } = render(
      wrap(
        <FiltersSidebar
          value={emptyFilters}
          onChange={() => {}}
          totalActive={0}
          onReset={() => {}}
        />,
      ),
    );
    const select = screen.getByLabelText('Сортировка');
    expect(select).toHaveTextContent('Сначала популярные');
    expect(select).not.toHaveTextContent('По релевантности');
    // Отдельного пункта «По популярности» больше нет (был дублем дефолта).
    expect(select).not.toHaveTextContent('По популярности');

    rerender(
      wrap(
        <FiltersSidebar
          value={emptyFilters}
          onChange={() => {}}
          totalActive={0}
          onReset={() => {}}
          hasQuery
        />,
      ),
    );
    expect(screen.getByLabelText('Сортировка')).toHaveTextContent('По релевантности');
  });

  it('фасет orig_lang рендерит секцию «Язык оригинала»; выбор дёргает onChange.srcLang', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      wrap(
        <FiltersSidebar
          value={emptyFilters}
          onChange={onChange}
          // lang и orig_lang (эффективный оригинал) — НЕЗАВИСИМЫЕ фасеты: en есть
          // только в orig_lang. Значение фильтра по-прежнему кладётся в srcLang.
          facets={{ lang: { ru: 12 }, orig_lang: { en: 4, fr: 1 } }}
          totalActive={0}
          onReset={() => {}}
        />,
      ),
    );
    expect(await screen.findByText('Язык оригинала')).toBeInTheDocument();
    // /api/languages в моках 404 → label = сам код. Опция 'en' существует
    // только в секции orig_lang (lang-фасет несёт лишь ru).
    await user.click(screen.getByRole('button', { name: /en\s*4/ }));
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ srcLang: 'en' }));
  });

  it('без src_lang-фасета секция «Язык оригинала» не рендерится', () => {
    render(
      wrap(
        <FiltersSidebar
          value={emptyFilters}
          onChange={() => {}}
          facets={{ lang: { ru: 12 } }}
          totalActive={0}
          onReset={() => {}}
        />,
      ),
    );
    expect(screen.queryByText('Язык оригинала')).not.toBeInTheDocument();
  });
});
