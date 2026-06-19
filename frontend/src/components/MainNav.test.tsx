import { describe, it, expect, vi } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MainNavBar, MainNavTrigger } from './MainNav';

/**
 * MainNav: проверяем наличие ссылок на разделы в десктоп-ряду (роль
 * «Главной» выполняет логотип, отдельного пункта нет — его быть не должно)
 * и то, что бургер открывает Sheet с тем же списком и клик по ссылке его
 * закрывает. Layout (порядок, видимость по брейкпоинту, hidden md:flex)
 * jsdom не считает (грабля №4) — это покрывает Playwright; здесь только
 * DOM-уровень.
 *
 * Реальный TanStack Link требует router-контекст — мокаем его простым
 * <a>, прокидывая activeProps в проп (нам важно лишь наличие href/onClick).
 */
vi.mock('@tanstack/react-router', () => ({
  Link: ({
    to,
    children,
    onClick,
    className,
  }: {
    to: string;
    children: React.ReactNode;
    onClick?: () => void;
    className?: string;
    activeProps?: unknown;
    activeOptions?: unknown;
  }) => (
    <a
      href={to}
      // preventDefault: в jsdom реальная навигация не реализована и
      // засоряет вывод warning'ом; нам важен лишь вызов onClick.
      onClick={(e) => {
        e.preventDefault();
        onClick?.();
      }}
      className={className}
    >
      {children}
    </a>
  ),
}));

const sections = ['Авторы', 'Книги', 'Жанры'];

describe('MainNav', () => {
  it('десктоп-ряд содержит ссылки на разделы (без отдельной «Главной»)', () => {
    render(<MainNavBar />);
    const nav = screen.getByRole('navigation', { name: 'Основная навигация' });
    for (const label of sections) {
      const link = within(nav).getByRole('link', { name: label });
      expect(link).toBeInTheDocument();
    }
    // Отдельного пункта «Главная» нет — его роль у логотипа в хэдере.
    expect(within(nav).queryByRole('link', { name: 'Главная' })).not.toBeInTheDocument();
    // Корректные href у разделов.
    expect(within(nav).getByRole('link', { name: 'Авторы' })).toHaveAttribute('href', '/authors');
    expect(within(nav).getByRole('link', { name: 'Книги' })).toHaveAttribute('href', '/books');
    expect(within(nav).getByRole('link', { name: 'Жанры' })).toHaveAttribute('href', '/genres');
  });

  it('бургер открывает Sheet со списком тех же разделов', async () => {
    const user = userEvent.setup();
    render(<MainNavTrigger />);
    // До клика список ссылок не отрендерен (Sheet закрыт).
    expect(screen.queryByRole('link', { name: 'Жанры' })).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Открыть меню' }));

    // После открытия все ссылки разделов доступны.
    for (const label of sections) {
      expect(await screen.findByRole('link', { name: label })).toBeInTheDocument();
    }
  });

  it('клик по ссылке в Sheet закрывает его', async () => {
    const user = userEvent.setup();
    render(<MainNavTrigger />);
    await user.click(screen.getByRole('button', { name: 'Открыть меню' }));
    const link = await screen.findByRole('link', { name: 'Книги' });

    await user.click(link);

    // Sheet закрылся → ссылки больше нет в DOM.
    expect(screen.queryByRole('link', { name: 'Книги' })).not.toBeInTheDocument();
  });
});
