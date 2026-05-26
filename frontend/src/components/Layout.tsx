import { Link, useNavigate } from '@tanstack/react-router';
import { BookOpen, LogOut, Settings, Shield, User as UserIcon } from 'lucide-react';
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
import { useMe, useLogout, type User } from '@/lib/auth';
import type { ReactNode } from 'react';

export function Layout({ children }: { children: ReactNode }) {
  const me = useMe();
  return (
    <div className="min-h-dvh flex flex-col">
      <Header user={me.data ?? null} />
      <main className="flex-1 mx-auto w-full max-w-6xl px-4 py-6">{children}</main>
    </div>
  );
}

function Header({ user }: { user: User | null }) {
  return (
    <header className="border-b border-border bg-background/95 backdrop-blur sticky top-0 z-10">
      <div className="mx-auto max-w-6xl px-4 h-14 flex items-center justify-between">
        <Link to="/" className="flex items-center gap-2 font-semibold tracking-tight">
          <BookOpen className="size-5 text-primary" aria-hidden />
          <span>skriptes</span>
        </Link>
        <div className="flex items-center gap-3">
          {user ? <CommandPalette /> : null}
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
        <DropdownMenuItem
          onSelect={(e) => {
            e.preventDefault();
            void navigate({ to: '/me' });
          }}
        >
          <Settings className="mr-2 size-4" aria-hidden />
          Профиль
        </DropdownMenuItem>
        {user.role === 'admin' ? (
          <DropdownMenuItem
            onSelect={(e) => {
              e.preventDefault();
              void navigate({ to: '/admin/users' });
            }}
          >
            <Shield className="mr-2 size-4" aria-hidden />
            Администрирование
          </DropdownMenuItem>
        ) : null}
        <DropdownMenuSeparator />
        <DropdownMenuItem
          onSelect={(e) => {
            e.preventDefault();
            logout.mutate();
          }}
          disabled={logout.isPending}
        >
          <LogOut className="mr-2 size-4" aria-hidden />
          Выйти
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
