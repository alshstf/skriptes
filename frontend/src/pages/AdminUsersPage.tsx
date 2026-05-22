import { useState } from 'react';
import {
  Shield,
  UserCircle2,
  Pencil,
  KeyRound,
  Trash2,
  Plus,
  Check,
  X,
} from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { BackButton } from '@/components/BackButton';
import {
  useAdminUsers,
  useCreateAdminUser,
  useUpdateAdminUser,
  useResetAdminUserPassword,
  useDeleteAdminUser,
  type AdminUser,
} from '@/lib/admin';
import { useMe, type Role } from '@/lib/auth';
import { ApiError } from '@/lib/api';

/**
 * AdminUsersPage — /admin/users. Доступна только админам (router guard
 * в beforeLoad плюс backend requireAdmin middleware).
 *
 * Структура: header + Card с таблицей пользователей + Card «Добавить».
 *
 * UI-решения:
 *   - Inline-edit для display_name (как у Kindle-target'ов). Email +
 *     role редактируются через ту же inline-форму (одной кнопкой
 *     «Изменить» → форма раскрывается со всеми тремя полями).
 *   - Reset password и Delete — отдельные кнопки с подтверждением
 *     через prompt/confirm (избегаем тяжёлой модалки; для homelab
 *     достаточно простого UI).
 */
export function AdminUsersPage() {
  const usersQ = useAdminUsers();
  const me = useMe();

  return (
    <article className="space-y-6">
      <BackButton />
      <header className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Управление пользователями</h1>
        <p className="text-sm text-muted-foreground">
          Список всех пользователей системы. Здесь можно добавить нового,
          сменить роль, сбросить пароль или удалить.
        </p>
      </header>

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-base">Пользователи</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2 pt-2">
          {usersQ.isLoading ? (
            <div className="space-y-2">
              <Skeleton className="h-12 w-full" />
              <Skeleton className="h-12 w-full" />
            </div>
          ) : usersQ.error ? (
            <p className="text-sm text-destructive">Не удалось загрузить список.</p>
          ) : (
            <UsersList users={usersQ.data ?? []} myId={me.data?.id ?? 0} />
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-base flex items-center gap-2">
            <Plus className="size-4" aria-hidden />
            Добавить пользователя
          </CardTitle>
        </CardHeader>
        <CardContent className="pt-2">
          <CreateUserForm />
        </CardContent>
      </Card>
    </article>
  );
}

function UsersList({ users, myId }: { users: AdminUser[]; myId: number }) {
  if (users.length === 0) {
    return <p className="text-sm italic text-muted-foreground">Список пуст.</p>;
  }
  return (
    <ul className="divide-y divide-border">
      {users.map((u) => (
        <li key={u.id} className="py-2">
          <UserRow user={u} isMe={u.id === myId} />
        </li>
      ))}
    </ul>
  );
}

function UserRow({ user, isMe }: { user: AdminUser; isMe: boolean }) {
  const [editing, setEditing] = useState(false);
  const [resetting, setResetting] = useState(false);
  const [email, setEmail] = useState(user.email);
  const [displayName, setDisplayName] = useState(user.display_name);
  const [role, setRole] = useState<Role>(user.role);

  const update = useUpdateAdminUser();
  const del = useDeleteAdminUser();

  if (editing) {
    return (
      <form
        className="grid gap-2 sm:grid-cols-[1fr_1fr_max-content_max-content_max-content]"
        onSubmit={async (e) => {
          e.preventDefault();
          try {
            await update.mutateAsync({
              id: user.id,
              email: email.trim() !== user.email ? email.trim() : undefined,
              display_name: displayName.trim() !== user.display_name ? displayName.trim() : undefined,
              role: role !== user.role ? role : undefined,
            });
            toast.success('Пользователь обновлён');
            setEditing(false);
          } catch {
            /* выведется ниже */
          }
        }}
      >
        <Input
          value={displayName}
          onChange={(e) => setDisplayName(e.target.value)}
          className="h-9"
          aria-label="Имя"
          placeholder="Имя"
        />
        <Input
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          className="h-9"
          type="email"
          aria-label="Email"
        />
        <RoleSelect value={role} onChange={setRole} />
        <Button type="submit" size="sm" disabled={update.isPending}>
          <Check className="size-4" aria-hidden />
          Сохранить
        </Button>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          onClick={() => {
            setEditing(false);
            setEmail(user.email);
            setDisplayName(user.display_name);
            setRole(user.role);
          }}
        >
          <X className="size-4" aria-hidden />
        </Button>
        {update.error ? (
          <p className="sm:col-span-5 text-xs text-destructive">
            {adminMessageOf(update.error)}
          </p>
        ) : null}
      </form>
    );
  }

  if (resetting) {
    return <ResetPasswordForm userId={user.id} email={user.email} onDone={() => setResetting(false)} />;
  }

  return (
    <div className="flex flex-wrap items-center gap-2">
      <div className="flex-1 min-w-0">
        <p className="text-sm font-medium flex items-center gap-2">
          {user.display_name}
          {user.role === 'admin' ? (
            <Shield className="size-3 text-primary" aria-label="admin" />
          ) : (
            <UserCircle2 className="size-3 text-muted-foreground" aria-label="user" />
          )}
          {isMe ? (
            <span className="text-[10px] uppercase tracking-wider text-muted-foreground">
              это вы
            </span>
          ) : null}
        </p>
        <p className="text-xs text-muted-foreground truncate">{user.email}</p>
      </div>
      <Button size="sm" variant="ghost" onClick={() => setEditing(true)} aria-label="Изменить">
        <Pencil className="size-4" aria-hidden />
      </Button>
      <Button
        size="sm"
        variant="ghost"
        onClick={() => setResetting(true)}
        aria-label="Сбросить пароль"
      >
        <KeyRound className="size-4" aria-hidden />
      </Button>
      <Button
        size="sm"
        variant="ghost"
        onClick={() => {
          if (isMe) {
            toast.error('Удалить самого себя нельзя.');
            return;
          }
          if (confirm(`Удалить пользователя «${user.display_name}» (${user.email})?`)) {
            del.mutate(user.id, {
              onSuccess: () => toast.success('Пользователь удалён'),
              onError: (e) => toast.error(adminMessageOf(e)),
            });
          }
        }}
        disabled={del.isPending || isMe}
        aria-label="Удалить"
        title={isMe ? 'Нельзя удалить самого себя' : 'Удалить'}
      >
        <Trash2 className="size-4 text-destructive" aria-hidden />
      </Button>
    </div>
  );
}

function ResetPasswordForm({
  userId,
  email,
  onDone,
}: {
  userId: number;
  email: string;
  onDone: () => void;
}) {
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const reset = useResetAdminUserPassword();

  const mismatch = confirm !== '' && next !== confirm;
  const tooShort = next.length > 0 && next.length < 8;
  const canSubmit = !reset.isPending && next.length >= 8 && next === confirm;

  return (
    <form
      className="space-y-2 rounded-md border border-border p-3"
      onSubmit={async (e) => {
        e.preventDefault();
        if (!canSubmit) return;
        try {
          await reset.mutateAsync({ id: userId, new_password: next });
          toast.success(`Пароль для ${email} обновлён. Все его сессии разлогинены.`);
          onDone();
        } catch {
          /* error ниже */
        }
      }}
    >
      <p className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
        Сбросить пароль для {email}
      </p>
      <div className="space-y-1">
        <Label htmlFor={`reset-new-${userId}`} className="text-xs">
          Новый пароль (мин. 8)
        </Label>
        <Input
          id={`reset-new-${userId}`}
          type="password"
          value={next}
          onChange={(e) => setNext(e.target.value)}
          className="h-9"
          autoComplete="new-password"
          required
        />
        {tooShort ? <p className="text-xs text-destructive">Минимум 8 символов.</p> : null}
      </div>
      <div className="space-y-1">
        <Label htmlFor={`reset-confirm-${userId}`} className="text-xs">
          Повторите
        </Label>
        <Input
          id={`reset-confirm-${userId}`}
          type="password"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          className="h-9"
          autoComplete="new-password"
          required
        />
        {mismatch ? <p className="text-xs text-destructive">Пароли не совпадают.</p> : null}
      </div>
      <div className="flex gap-2">
        <Button type="submit" size="sm" disabled={!canSubmit}>
          Сбросить
        </Button>
        <Button type="button" size="sm" variant="ghost" onClick={onDone}>
          Отмена
        </Button>
      </div>
      {reset.error ? (
        <p className="text-xs text-destructive">{adminMessageOf(reset.error)}</p>
      ) : null}
    </form>
  );
}

function CreateUserForm() {
  const [email, setEmail] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [password, setPassword] = useState('');
  const [role, setRole] = useState<Role>('user');
  const create = useCreateAdminUser();

  const tooShort = password.length > 0 && password.length < 8;
  const canSubmit =
    !create.isPending && email.includes('@') && password.length >= 8;

  return (
    <form
      className="space-y-2"
      onSubmit={async (e) => {
        e.preventDefault();
        if (!canSubmit) return;
        try {
          await create.mutateAsync({
            email: email.trim(),
            display_name: displayName.trim() || undefined,
            password,
            role,
          });
          toast.success(`Пользователь ${email} добавлен`);
          setEmail('');
          setDisplayName('');
          setPassword('');
          setRole('user');
        } catch {
          /* error ниже */
        }
      }}
    >
      <div className="grid gap-2 sm:grid-cols-2">
        <div className="space-y-1">
          <Label htmlFor="new-email" className="text-xs">Email</Label>
          <Input
            id="new-email"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            className="h-9"
            autoComplete="off"
            required
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="new-name" className="text-xs">Имя (опционально)</Label>
          <Input
            id="new-name"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            className="h-9"
            placeholder="Берётся из email если пусто"
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="new-password" className="text-xs">
            Пароль (мин. 8)
          </Label>
          <Input
            id="new-password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className="h-9"
            autoComplete="new-password"
            required
          />
          {tooShort ? (
            <p className="text-xs text-destructive">Минимум 8 символов.</p>
          ) : null}
        </div>
        <div className="space-y-1">
          <Label htmlFor="new-role" className="text-xs">Роль</Label>
          <RoleSelect value={role} onChange={setRole} id="new-role" />
        </div>
      </div>
      <Button type="submit" size="sm" disabled={!canSubmit}>
        Добавить
      </Button>
      {create.error ? (
        <p className="text-xs text-destructive">{adminMessageOf(create.error)}</p>
      ) : null}
    </form>
  );
}

/**
 * RoleSelect — простой <select> на admin / user. shadcn Select удобнее
 * визуально, но в форме на 2-х опциях overkill; нативный select легче
 * для accessibility и не требует state-управления Popover'ом.
 */
function RoleSelect({
  value,
  onChange,
  id,
}: {
  value: Role;
  onChange: (r: Role) => void;
  id?: string;
}) {
  return (
    <select
      id={id}
      value={value}
      onChange={(e) => onChange(e.target.value as Role)}
      className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
      aria-label="Роль"
    >
      <option value="user">user</option>
      <option value="admin">admin</option>
    </select>
  );
}

function adminMessageOf(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 409) {
      // body может содержать "cannot delete self" / "cannot demote the last admin" / "email already taken"
      if (/last admin/.test(err.message)) {
        return 'Нельзя удалить или деградировать последнего админа.';
      }
      if (/email already taken/.test(err.message)) {
        return 'Этот email уже занят другим пользователем.';
      }
      if (/cannot delete self/.test(err.message)) {
        return 'Нельзя удалить самого себя через этот UI.';
      }
      return err.message;
    }
    if (err.status === 400) return 'Проверьте поля: возможно, email неправильный или пароль короче 8 символов.';
    if (err.status === 404) return 'Пользователь не найден (возможно, удалён в другом окне).';
    return err.message;
  }
  return 'Не удалось выполнить.';
}
