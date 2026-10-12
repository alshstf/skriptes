import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AuthorLifeContext } from './AuthorLifeContext';

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

const lifeContext = {
  available: true,
  author: { id: 22, name: 'Достоевский Фёдор Михайлович' },
  period: { from: 1867, to: 1869 },
  items: [
    {
      event: { key: 'loss-1868', date: { from: '1868-05-24', precision: 'day' }, type: 'loss', tier: 'major', short: 'Смерть трёхмесячной дочери Сони', long: 'Умерла от воспаления лёгких.', place: { label: 'Женева' } },
      relation: { kind: 'during' },
      linked: true,
      note: 'работа над романом шла в эти недели',
    },
    {
      event: { key: 'poverty-1865', date: { from: '1865-07', precision: 'month' }, type: 'poverty', tier: 'major', short: 'Долги и кабальный договор со Стелловским' },
      relation: { kind: 'before', years: 2 },
      linked: false,
    },
  ],
};

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{ui}</QueryClientProvider>;
}

describe('AuthorLifeContext', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('рендерит окно: даты, формулировки, связи, пометки, ссылку на таймлайн', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify(lifeContext), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    render(wrap(<AuthorLifeContext workId={89} />));
    expect(await screen.findByText('В жизни автора в это время')).toBeInTheDocument();
    expect(screen.getByText('· писалась в 1867–1869')).toBeInTheDocument();
    expect(screen.getByText('24 мая 1868')).toBeInTheDocument();
    expect(screen.getByText('июль 1865')).toBeInTheDocument();
    expect(screen.getByText('во время работы над книгой')).toBeInTheDocument();
    expect(screen.getByText('за 2 года до')).toBeInTheDocument();
    expect(screen.getByText('работа над романом шла в эти недели')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Вся жизнь и книги →' })).toHaveAttribute('href', '/authors/22/timeline');
  });

  it('ничего не рендерит, когда окно пустое или работа без id', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ available: false }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    const { container } = render(wrap(<AuthorLifeContext workId={89} />));
    await new Promise((r) => setTimeout(r, 20));
    expect(container).toBeEmptyDOMElement();
    const { container: c2 } = render(wrap(<AuthorLifeContext workId={undefined} />));
    expect(c2).toBeEmptyDOMElement();
  });
});
