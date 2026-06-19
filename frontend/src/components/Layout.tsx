import { Link, useNavigate } from '@tanstack/react-router';
import { BookOpen, Library, LogOut, Settings, Shield, User as UserIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { CommandPalette } from '@/components/CommandPalette';
import { MainNavBar, MainNavTrigger } from '@/components/MainNav';
import { useMe, useLogout, type User } from '@/lib/auth';
import { useAppearance } from '@/lib/appearance';
import { HeroSearchContext } from '@/lib/heroSearch';
import { cn } from '@/lib/utils';
import { useState, type ReactNode } from 'react';

export function Layout({ children }: { children: ReactNode }) {
  const me = useMe();
  // Подтягиваем серверную настройку внешнего вида и зеркалим в localStorage,
  // чтобы стиль чипов применился на любой странице (даже до захода в профиль).
  useAppearance();
  // Видим ли hero-поиск Главной — управляет видимостью кнопки поиска в хэдере
  // (см. heroSearch.ts). Дефолт false: на остальных страницах кнопка видна.
  const [heroSearchVisible, setHeroSearchVisible] = useState(false);
  return (
    <HeroSearchContext.Provider value={{ heroSearchVisible, setHeroSearchVisible }}>
      <div className="min-h-dvh flex flex-col">
        <Header user={me.data ?? null} heroSearchVisible={heroSearchVisible} />
        <main className="flex-1 mx-auto w-full max-w-6xl px-4 py-6">{children}</main>
      </div>
    </HeroSearchContext.Provider>
  );
}

function Header({ user, heroSearchVisible }: { user: User | null; heroSearchVisible: boolean }) {
  return (
    <header className="border-b border-border bg-background/95 backdrop-blur sticky top-0 z-10">
      <div className="mx-auto max-w-6xl px-4 h-14 flex items-center justify-between gap-2">
        <div className="flex items-center gap-4 min-w-0">
          {/* Бургер — только мобила (md:hidden внутри компонента), слева от логотипа */}
          {user ? <MainNavTrigger /> : null}
          <Link to="/" className="flex items-center gap-2 font-semibold tracking-tight">
            <BookOpen className="size-5 text-primary" aria-hidden />
            <span>skriptes</span>
          </Link>
          {/* Десктоп-ряд разделов — после логотипа (hidden md:flex внутри компонента) */}
          {user ? <MainNavBar /> : null}
        </div>
        <div className="flex items-center gap-3">
          {/* Поиск в хэдере прячется, пока на Главной виден hero-инпут, и
              быстро «въезжает» при скролле вниз. Остаётся смонтированным
              (Cmd+K работает всегда), скрываем визуально. */}
          {user ? (
            <div
              className={cn(
                'transition-all duration-200',
                heroSearchVisible
                  ? 'pointer-events-none -translate-y-1 opacity-0'
                  : 'translate-y-0 opacity-100',
              )}
              aria-hidden={heroSearchVisible}
            >
              <CommandPalette />
            </div>
          ) : null}
          {user ? <UserMenu user={user} /> : null}
        </div>
      </div>
    </header>
  );
}

function UserMenu({ user }: { user: User }) {
  const logout = useLogout();
  const navigate = useNavigate();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="sm" className="gap-2">
          <UserIcon className="size-4" aria-hidden />
          <span className="hidden sm:inline">{user.display_name}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuLabel className="font-normal">
          <div className="flex flex-col">
            <span className="text-sm font-medium">{user.display_name}</span>
            <span className="text-xs text-muted-foreground">{user.email}</span>
          </div>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => void navigate({ to: '/shelves' })}>
          <Library className="mr-2 size-4" aria-hidden />
          Мои полки
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => void navigate({ to: '/me' })}>
          <Settings className="mr-2 size-4" aria-hidden />
          Профиль
        </DropdownMenuItem>
        {user.role === 'admin' ? (
          <DropdownMenuItem onSelect={() => void navigate({ to: '/admin/users' })}>
            <Shield className="mr-2 size-4" aria-hidden />
            Администрирование
          </DropdownMenuItem>
        ) : null}
        <DropdownMenuSeparator />
        <DropdownMenuItem
          onSelect={() => logout.mutate()}
          disabled={logout.isPending}
        >
          <LogOut className="mr-2 size-4" aria-hidden />
          Выйти
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
