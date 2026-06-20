import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { RatingControl } from './RatingControl';

describe('RatingControl', () => {
  it('renders 5 clickable number cells in interactive mode', () => {
    render(<RatingControl value={0} onChange={() => {}} />);
    expect(screen.getAllByRole('button')).toHaveLength(5);
    expect(screen.getByLabelText('Оценить на 3')).toBeInTheDocument();
  });

  it('clicking a cell sets that rating', () => {
    const onChange = vi.fn();
    render(<RatingControl value={0} onChange={onChange} />);
    fireEvent.click(screen.getByLabelText('Оценить на 4'));
    expect(onChange).toHaveBeenCalledWith(4);
  });

  it('clicking the current rating clears it (null)', () => {
    const onChange = vi.fn();
    render(<RatingControl value={3} onChange={onChange} />);
    fireEvent.click(screen.getByLabelText('Снять оценку (сейчас 3)'));
    expect(onChange).toHaveBeenCalledWith(null);
  });

  it('readOnly mode renders no buttons and exposes aria value', () => {
    render(<RatingControl value={4} readOnly />);
    expect(screen.queryAllByRole('button')).toHaveLength(0);
    expect(screen.getByLabelText('Оценка 4 из 5')).toBeInTheDocument();
  });

  it('without onChange falls back to read-only (no buttons)', () => {
    render(<RatingControl value={2} />);
    expect(screen.queryAllByRole('button')).toHaveLength(0);
  });
});
