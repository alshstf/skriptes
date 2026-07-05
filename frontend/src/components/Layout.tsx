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
import { useVersion, formatCollectionVersion } from '@/lib/version';
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
    // bg-background СПЛОШНОЙ (не /95 + blur): на iOS Safari backdrop-blur ненадёжен,
    // и при скролле контент просвечивал сквозь полупрозрачный хэдер.
    // shadow + граница: хэдер, контент и боди — один цвет (--background), без тени
    // граница (--border = 10% white) почти невидима → при скролле хэдер визуально
    // «сливался» с контентом. Мягкая тень даёт глубину (box-shadow на iOS Safari
    // надёжен в отличие от blur), хэдер читается как отдельный слой над контентом.
    // pt-safe: в PWA standalone на iOS (status-bar-style=black-translucent) контент
    // иначе лезет под системный бар с часами; на десктопе/в Safari инсет = 0.
    <header
      className="sticky top-0 z-20 border-b border-border bg-background pt-safe shadow-[0_10px_22px_-10px_rgba(0,0,0,0.9)]"
    >
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

// MenuVersion — ненавязчивая версия в подвале меню пользователя (dropdown под
// аватаром). НЕ футер страницы: на контентных `/books` с бесконечным скроллом
// низ страницы недостижим (следующий чанк догружается раньше). Меню же доступно
// с любой страницы и открывается осознанно — версия всегда под рукой.
function MenuVersion() {
  const { data } = useVersion();
  if (!data?.version) return null;
  // Версия коллекции: приоритет — version.info нового INPX; если его ещё нет
  // (коллекция импортирована до фичи, а импорт с тех пор пропускался как
  // неизменный) — фолбэк на дату последнего импорта («от …»), чтобы понять,
  // подтянулся ли новый INPX.
  const coll = data.collection_version
    ? `коллекция ${formatCollectionVersion(data.collection_version)}`
    : data.collection_imported_at
      ? `коллекция от ${data.collection_imported_at.slice(0, 10)}`
      : null;
  return (
    <>
      <DropdownMenuSeparator />
      <div className="px-2 py-1.5 text-xs leading-relaxed text-muted-foreground/70 select-text">
        <div>skriptes {data.version}</div>
        {coll ? <div>{coll}</div> : null}
      </div>
    </>
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
        <MenuVersion />
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
