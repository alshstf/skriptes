import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { LoginPage } from './LoginPage';

const navigateMock = vi.fn();
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigateMock,
}));

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return <QueryClientProvider client={qc}>{ui}</QueryClientProvider>;
}

describe('LoginPage', () => {
  beforeEach(() => {
    navigateMock.mockReset();
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('logs in on success and navigates home', async () => {
    const user = userEvent.setup();
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(
          JSON.stringify({ user: { id: 1, email: 'me@x.com', display_name: 'Me', role: 'admin', created_at: '' } }),
          { status: 200, headers: { 'content-type': 'application/json' } },
        ),
      ),
    );
    render(wrap(<LoginPage />));
    await user.type(screen.getByLabelText('Email'), 'me@x.com');
    await user.type(screen.getByLabelText('Пароль'), 'secret123');
    await user.click(screen.getByRole('button', { name: 'Войти' }));
    await waitFor(() => expect(navigateMock).toHaveBeenCalledWith({ to: '/' }));
  });

  it('shows russian error on 401 and does not navigate', async () => {
    const user = userEvent.setup();
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(JSON.stringify({ error: 'invalid email or password' }), {
          status: 401,
          headers: { 'content-type': 'application/json' },
        }),
      ),
    );
    render(wrap(<LoginPage />));
    await user.type(screen.getByLabelText('Email'), 'me@x.com');
    await user.type(screen.getByLabelText('Пароль'), 'wrong');
    await user.click(screen.getByRole('button', { name: 'Войти' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Неверный email или пароль');
    expect(navigateMock).not.toHaveBeenCalled();
  });
});
