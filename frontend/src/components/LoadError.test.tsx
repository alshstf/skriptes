import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';

import { LoadError } from './LoadError';
import { ApiError } from '@/lib/api';

describe('LoadError', () => {
  it('отказ «только из домашней сети» — причина и что делать, а не «не удалось загрузить»', () => {
    const err = new ApiError(403, { error: 'администрирование доступно только из домашней сети' },
      'администрирование доступно только из домашней сети');
    render(<LoadError error={err}>Не удалось загрузить список.</LoadError>);
    expect(screen.getByRole('alert')).toHaveTextContent('Администрирование доступно только из домашней сети.');
    expect(screen.getByText(/Частный узел iCloud/)).toBeInTheDocument();
    expect(screen.queryByText('Не удалось загрузить список.')).not.toBeInTheDocument();
  });

  it('другой 403 с причиной — только причина, без подсказки про сеть', () => {
    render(<LoadError error={new ApiError(403, { error: 'forbidden' }, 'forbidden')}>Не удалось.</LoadError>);
    expect(screen.getByRole('alert')).toHaveTextContent('Forbidden.');
    expect(screen.queryByText(/Частный узел/)).not.toBeInTheDocument();
  });

  it('прочие ошибки — прежний текст', () => {
    render(<LoadError error={new ApiError(500, null, 'HTTP 500')}>Не удалось загрузить список.</LoadError>);
    expect(screen.getByRole('alert')).toHaveTextContent('Не удалось загрузить список.');
    render(<LoadError error={new ApiError(403, null, 'HTTP 403')}>Не удалось загрузить настройки.</LoadError>);
    expect(screen.getByText('Не удалось загрузить настройки.')).toBeInTheDocument();
  });
});
