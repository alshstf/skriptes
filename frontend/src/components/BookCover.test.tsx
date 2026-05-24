import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { BookCover } from './BookCover';

describe('BookCover', () => {
  it('renders <img> with /api/covers/{path} when cover_path is set', () => {
    render(<BookCover coverPath="abc123.jpg" title="Test Book" />);
    const img = screen.getByRole('img', { name: /Обложка: Test Book/ });
    expect(img.tagName).toBe('IMG');
    expect(img).toHaveAttribute('src', '/api/covers/abc123.jpg');
  });

  it('renders placeholder with title when cover_path is missing', () => {
    render(<BookCover title="Без обложки" />);
    // Placeholder экспонируется как role="img" с aria-label.
    const placeholder = screen.getByRole('img', { name: /Без обложки.*загружается/ });
    expect(placeholder.tagName).not.toBe('IMG'); // div с role
    // Заголовок виден в placeholder'е.
    expect(screen.getByText('Без обложки')).toBeInTheDocument();
  });

  it('renders monogram placeholder (первая буква) when placeholder="monogram"', () => {
    render(<BookCover title="Гост" placeholder="monogram" />);
    const ph = screen.getByRole('img', { name: 'Обложка: Гост' });
    expect(ph.tagName).not.toBe('IMG'); // div с role
    expect(ph).toHaveTextContent('Г'); // первая буква названия
    // монограм НЕ показывает «загружается» — это финальное состояние, а
    // не индикатор загрузки.
    expect(screen.queryByText(/загружается/)).not.toBeInTheDocument();
  });

  it('keeps same aspect class so swap does not shift layout', () => {
    const { rerender, container } = render(<BookCover title="X" />);
    const placeholderClass = container.firstElementChild?.className ?? '';
    rerender(<BookCover coverPath="foo.jpg" title="X" />);
    const imgClass = container.firstElementChild?.className ?? '';
    // aspect-[2/3] должен быть в обоих случаях.
    expect(placeholderClass).toContain('aspect-[2/3]');
    expect(imgClass).toContain('aspect-[2/3]');
  });
});
