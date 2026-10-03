import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { KindleSenderHint } from './KindleSenderHint';

const { toastSuccess } = vi.hoisted(() => ({ toastSuccess: vi.fn() }));
vi.mock('sonner', () => ({ toast: { success: toastSuccess, error: vi.fn() } }));

describe('KindleSenderHint', () => {
  afterEach(() => vi.clearAllMocks());

  it('показывает адрес и копирует его', async () => {
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue();
    render(<KindleSenderHint sender="books@example.com" />);
    expect(screen.getByText('books@example.com')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Скопировать адрес отправителя' }));
    expect(writeText).toHaveBeenCalledWith('books@example.com');
    expect(toastSuccess).toHaveBeenCalled();
  });

  it('по нажатию открывает шаги в Amazon', async () => {
    const user = userEvent.setup();
    render(<KindleSenderHint sender="books@example.com" />);
    await user.click(screen.getByRole('button', { name: /Как разрешить его в Amazon/ }));
    expect(await screen.findByText(/Approved Personal Document E-mail List/)).toBeInTheDocument();
    // адрес повторён в шаге 3: поповер может закрыть строку с ним
    expect(screen.getAllByText('books@example.com')).toHaveLength(2);
    expect(screen.getByRole('link', { name: 'amazon.com/mycd' })).toHaveAttribute(
      'href',
      'https://www.amazon.com/mycd',
    );
  });

  it('без адреса — отправка не настроена, подсказки нет', () => {
    render(<KindleSenderHint sender="" />);
    expect(screen.getByText(/Отправка на Kindle на сервере пока не настроена/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Как разрешить/ })).toBeNull();
  });
});
