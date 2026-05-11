import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { FiltersSidebar, type FiltersValue } from './FiltersSidebar';

const emptyFilters: FiltersValue = {
  genres: [],
  lang: '',
  yearFrom: 0,
  yearTo: 0,
  sort: '',
};

describe('FiltersSidebar', () => {
  it('renders genre checkboxes with counts from facets', () => {
    render(
      <FiltersSidebar
        value={emptyFilters}
        onChange={() => {}}
        facets={{ genres: { sf_action: 4, fantasy: 2 }, lang: { ru: 6 } }}
        totalActive={0}
        onReset={() => {}}
      />,
    );
    // Жанры в порядке убывания count: sf_action (4) первым.
    const genreLabels = screen.getAllByText(/sf_action|fantasy/);
    expect(genreLabels[0]).toHaveTextContent('sf_action');
    expect(screen.getByText('4')).toBeInTheDocument();
    expect(screen.getByText('2')).toBeInTheDocument();
  });

  it('calls onChange with updated genres when checkbox flipped', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <FiltersSidebar
        value={emptyFilters}
        onChange={onChange}
        facets={{ genres: { sf_action: 4 } }}
        totalActive={0}
        onReset={() => {}}
      />,
    );
    await user.click(screen.getByRole('checkbox', { name: /sf_action/ }));
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ genres: ['sf_action'] }));
  });

  it('reset button appears only when there are active filters', async () => {
    const user = userEvent.setup();
    const onReset = vi.fn();
    const { rerender } = render(
      <FiltersSidebar value={emptyFilters} onChange={() => {}} totalActive={0} onReset={onReset} />,
    );
    expect(screen.queryByRole('button', { name: /Сбросить/ })).not.toBeInTheDocument();

    rerender(
      <FiltersSidebar
        value={{ ...emptyFilters, genres: ['sf_action'] }}
        onChange={() => {}}
        totalActive={1}
        onReset={onReset}
      />,
    );
    const reset = screen.getByRole('button', { name: /Сбросить/ });
    await user.click(reset);
    expect(onReset).toHaveBeenCalled();
  });

  it('selecting a sort option triggers onChange', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <FiltersSidebar value={emptyFilters} onChange={onChange} totalActive={0} onReset={() => {}} />,
    );
    await user.selectOptions(screen.getByLabelText('Сортировка'), 'year_desc');
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ sort: 'year_desc' }));
  });
});
