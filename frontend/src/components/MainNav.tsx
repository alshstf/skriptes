import { Link } from '@tanstack/react-router';
import { BookOpen, Menu, Tags, Users } from 'lucide-react';
import { useState } from 'react';
import { Button } from '@/components/ui/button';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from '@/components/ui/sheet';
import { cn } from '@/lib/utils';

/**
 * Primary-навигация по разделам приложения: Авторы / Книги / Жанры. Роль
 * «Главной» (`/`) выполняет клик по логотипу skriptes в хэдере — отдельного
 * пункта нет. Два экспорта под две точки хедера (разный порядок на десктопе
 * и мобиле):
 *   - MainNavBar — горизонтальный ряд ссылок после логотипа (`hidden md:flex`);
 *   - MainNavTrigger — бургер слева от логотипа (`md:hidden`), открывает Sheet
 *     с тем же списком вертикально.
 *
 * Active-состояние ссылки даёт TanStack Router через activeProps.
 */

type NavItem = {
  to: string;
  label: string;
  icon: typeof Users;
  exact?: boolean;
};

const navItems: NavItem[] = [
  { to: '/authors', label: 'Авторы', icon: Users },
  { to: '/books', label: 'Книги', icon: BookOpen },
  { to: '/genres', label: 'Жанры', icon: Tags },
];

// Базовый стиль ссылки (десктоп) — приглушённый текст, подсветка на hover.
const desktopBase =
  'inline-flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-sm font-medium text-muted-foreground transition-colors hover:text-foreground';
// Активный раздел — контрастный текст.
const desktopActive = 'text-foreground';

// MainNavTrigger — бургер для мобилы (рендерится слева от логотипа).
// На десктопе скрыт; десктопный ряд ссылок — отдельный MainNavBar.
export function MainNavTrigger() {
  return <MobileNav />;
}

// MainNavBar — горизонтальный ряд ссылок, рендерится после логотипа.
// Скрыт на мобиле (там навигация живёт в Sheet за бургером).
export function MainNavBar() {
  return (
    <nav className="hidden md:flex items-center gap-1" aria-label="Основная навигация">
      {navItems.map((item) => {
        const Icon = item.icon;
        return (
          <Link
            key={item.to}
            to={item.to}
            activeOptions={item.exact ? { exact: true } : undefined}
            className={desktopBase}
            activeProps={{ className: cn(desktopBase, desktopActive) }}
          >
            <Icon className="size-4" aria-hidden />
            <span>{item.label}</span>
          </Link>
        );
      })}
    </nav>
  );
}

// Базовый стиль ссылки в мобильном Sheet — крупнее, во всю ширину.
const mobileBase =
  'flex items-center gap-3 rounded-md px-3 py-2.5 text-sm font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground';
const mobileActive = 'bg-accent text-foreground';

// MobileNav — бургер + Sheet, виден только на мобиле. Клик по ссылке
// закрывает Sheet (контролируемое open-состояние).
function MobileNav() {
  const [open, setOpen] = useState(false);
  return (
    <div className="md:hidden">
      <Sheet open={open} onOpenChange={setOpen}>
        <SheetTrigger asChild>
          <Button variant="ghost" size="icon-sm" aria-label="Открыть меню">
            <Menu className="size-5" aria-hidden />
          </Button>
        </SheetTrigger>
        <SheetContent side="left" className="w-64">
          <SheetHeader>
            <SheetTitle>Навигация</SheetTitle>
            <SheetDescription className="sr-only">Разделы приложения</SheetDescription>
          </SheetHeader>
          <nav className="flex flex-col gap-1 px-2" aria-label="Основная навигация">
            {navItems.map((item) => {
              const Icon = item.icon;
              return (
                <Link
                  key={item.to}
                  to={item.to}
                  activeOptions={item.exact ? { exact: true } : undefined}
                  className={mobileBase}
                  activeProps={{ className: cn(mobileBase, mobileActive) }}
                  onClick={() => setOpen(false)}
                >
                  <Icon className="size-4" aria-hidden />
                  <span>{item.label}</span>
                </Link>
              );
            })}
          </nav>
        </SheetContent>
      </Sheet>
    </div>
  );
}
