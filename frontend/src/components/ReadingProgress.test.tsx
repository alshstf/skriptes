import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ReadingProgress } from './ReadingProgress';

describe('ReadingProgress', () => {
  it('renders nothing when total is 0', () => {
    const { container } = render(<ReadingProgress read={0} total={0} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders "Скачано X из Y" + correct percentage', () => {
    render(<ReadingProgress read={3} total={12} />);
    expect(screen.getByText(/Скачано 3 из 12/)).toBeInTheDocument();
    expect(screen.getByText('25%')).toBeInTheDocument();
    const bar = screen.getByRole('progressbar');
    expect(bar).toHaveAttribute('aria-valuenow', '3');
    expect(bar).toHaveAttribute('aria-valuemax', '12');
  });

  it('caps percentage at 100 when read > total (edge case)', () => {
    render(<ReadingProgress read={15} total={10} />);
    expect(screen.getByText('100%')).toBeInTheDocument();
  });

  it('uses correct Russian plural for total', () => {
    render(<ReadingProgress read={1} total={1} />);
    // 1 → "книги" в нашем варианте (Скачано 1 из 1 книги)
    expect(screen.getByText(/Скачано 1 из 1 книги/)).toBeInTheDocument();
  });
});
