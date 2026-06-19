import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { RatingStars } from './RatingStars';

describe('RatingStars', () => {
  it('renders 5 clickable star buttons in interactive mode', () => {
    render(<RatingStars value={0} onChange={() => {}} />);
    expect(screen.getAllByRole('button')).toHaveLength(5);
    expect(screen.getByLabelText('Оценить на 3')).toBeInTheDocument();
  });

  it('clicking a star sets that rating', () => {
    const onChange = vi.fn();
    render(<RatingStars value={0} onChange={onChange} />);
    fireEvent.click(screen.getByLabelText('Оценить на 4'));
    expect(onChange).toHaveBeenCalledWith(4);
  });

  it('clicking the current rating clears it (null)', () => {
    const onChange = vi.fn();
    render(<RatingStars value={3} onChange={onChange} />);
    // Текущая оценка 3 → её кнопка предлагает снять.
    fireEvent.click(screen.getByLabelText('Снять оценку (сейчас 3)'));
    expect(onChange).toHaveBeenCalledWith(null);
  });

  it('readOnly mode renders no buttons and exposes aria value', () => {
    render(<RatingStars value={4} readOnly />);
    expect(screen.queryAllByRole('button')).toHaveLength(0);
    expect(screen.getByLabelText('Оценка 4 из 5')).toBeInTheDocument();
  });

  it('without onChange falls back to read-only (no buttons)', () => {
    render(<RatingStars value={2} />);
    expect(screen.queryAllByRole('button')).toHaveLength(0);
  });
});
