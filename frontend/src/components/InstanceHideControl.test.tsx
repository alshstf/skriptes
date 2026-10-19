import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { InstanceHideControl } from './InstanceHideControl';

vi.mock('@tanstack/react-router', () => ({
  Link: ({ to, params, children, className }: { to: string; params?: Record<string, string>; children: React.ReactNode; className?: string }) => {
    let href = to;
    for (const [k, v] of Object.entries(params ?? {})) href = href.replace(`$${k}`, v);
    return (
      <a href={href} className={className}>
        {children}
      </a>
    );
  },
}));

const auth = vi.hoisted(() => ({ role: 'admin' as 'admin' | 'user', edit: true }));
vi.mock('@/lib/auth', () => ({ useMe: () => ({ data: { id: 1, role: auth.role } }) }));
vi.mock('@/lib/editMode', () => ({ useCanEdit: () => auth.role === 'admin' && auth.edit }));

function routes(map: Record<string, unknown>) {
  return vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    const key = Object.keys(map).find((k) => url.includes(k));
    return new Response(JSON.stringify(key ? map[key] : {}), { status: 200, headers: { 'Content-Type': 'application/json' } });
  });
}

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{ui}</QueryClientProvider>;
}

const visible = { hidden: false, explicit: false, via: [] };

describe('InstanceHideControl', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    auth.role = 'admin';
    auth.edit = true;
  });

  it('читателю — ничего (статус даже не запрашивается)', async () => {
    auth.role = 'user';
    const f = routes({});
    const { container } = render(wrap(<InstanceHideControl kind="author" id={5} name="Гитлер" />));
    await new Promise((r) => setTimeout(r, 20));
    expect(container).toBeEmptyDOMElement();
    expect(f).not.toHaveBeenCalled();
  });

  it('админ в режиме правки — кнопка «Скрыть на инстансе»; вне режима — ничего', async () => {
    routes({ '/api/admin/authors/5/hidden-status': visible });
    render(wrap(<InstanceHideControl kind="author" id={5} name="Гитлер" />));
    expect(await screen.findByRole('button', { name: /Скрыть на инстансе/ })).toBeInTheDocument();
    auth.edit = false;
    const { container } = render(wrap(<InstanceHideControl kind="author" id={5} name="Гитлер" />));
    await new Promise((r) => setTimeout(r, 20));
    expect(container).toBeEmptyDOMElement();
  });

  it('скрытый автор — плашка и «Вернуть»', async () => {
    routes({ '/api/admin/authors/5/hidden-status': { hidden: true, explicit: true, via: [] } });
    render(wrap(<InstanceHideControl kind="author" id={5} name="Гитлер" />));
    expect(await screen.findByText('Скрыто на инстансе.')).toBeInTheDocument();
    expect(screen.getByText(/этого автора и его книги/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Вернуть' })).toBeInTheDocument();
  });

  it('работа скрыта из-за автора — ссылка на автора, «Вернуть» нет', async () => {
    routes({
      '/api/admin/works/9/hidden-status': { hidden: true, explicit: false, via: [{ kind: 'author', id: 5, name: 'Гитлер Адольф' }] },
      '/api/admin/books/77/hidden-status': { hidden: true, explicit: false, via: [] },
    });
    render(wrap(<InstanceHideControl kind="work" id={9} name="Книга" bookId={77} />));
    expect(await screen.findByRole('link', { name: 'Гитлер Адольф' })).toHaveAttribute('href', '/authors/5');
    expect(screen.queryByRole('button', { name: 'Вернуть' })).not.toBeInTheDocument();
  });

  it('работа видна, открытое издание скрыто — плашка издания', async () => {
    routes({
      '/api/admin/works/9/hidden-status': visible,
      '/api/admin/books/77/hidden-status': { hidden: true, explicit: false, via: [{ kind: 'author', id: 5, name: 'Соавтор' }] },
    });
    render(wrap(<InstanceHideControl kind="work" id={9} name="Антология" bookId={77} />));
    expect(await screen.findByText(/это издание/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Соавтор' })).toBeInTheDocument();
  });
});
