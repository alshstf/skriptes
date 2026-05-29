import { describe, it, expect } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
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

  it('renders <img> from src (on-demand by-id)', () => {
    render(<BookCover src="/api/covers/book/42" title="Test Book" />);
    const img = screen.getByRole('img', { name: /Обложка: Test Book/ });
    expect(img.tagName).toBe('IMG');
    expect(img).toHaveAttribute('src', '/api/covers/book/42');
  });

  it('падает на монограм-плейсхолдер при ошибке загрузки (404 by-id)', () => {
    render(<BookCover src="/api/covers/book/99" title="Гост" placeholder="monogram" />);
    const img = screen.getByRole('img', { name: 'Обложка: Гост' });
    expect(img.tagName).toBe('IMG');
    fireEvent.error(img); // картинки нет (404) → onError
    const ph = screen.getByRole('img', { name: 'Обложка: Гост' });
    expect(ph.tagName).not.toBe('IMG'); // теперь div-плейсхолдер
    expect(ph).toHaveTextContent('Г');
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

  it('повторный маунт после 404 сразу рисует плейсхолдер (кэш исхода, без мелькания)', () => {
    const url = '/api/covers/book/777';
    const first = render(<BookCover src={url} title="Зет" placeholder="monogram" />);
    fireEvent.error(screen.getByRole('img', { name: 'Обложка: Зет' }));
    expect(screen.getByRole('img', { name: 'Обложка: Зет' }).tagName).not.toBe('IMG');
    first.unmount();

    // Тот же url появляется снова (как при возврате строки в окно
    // виртуализации) — сразу плейсхолдер, без повторной попытки <img>.
    render(<BookCover src={url} title="Зет" placeholder="monogram" />);
    const ph = screen.getByRole('img', { name: 'Обложка: Зет' });
    expect(ph.tagName).not.toBe('IMG');
    expect(ph).toHaveTextContent('З');
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
