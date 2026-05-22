import { useState } from 'react';
import { Mail, Trash2, Pencil, Check, X, User as UserIcon, KeyRound } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { BackButton } from '@/components/BackButton';
import {
  useKindleTargets,
  useAddKindleTarget,
  useUpdateKindleTarget,
  useDeleteKindleTarget,
  type KindleTarget,
} from '@/lib/kindle';
import { useMe, useUpdateMe, useChangeMyPassword, type User } from '@/lib/auth';
import { ApiError } from '@/lib/api';

/**
 * ProfilePage — настройки пользователя. Сейчас только Kindle-адресаты
 * (для Send-to-Kindle), позже сюда лягут другие настройки (theme, и т.д.).
 *
 * Адресаты:
 *  - таблица c существующими (inline-edit для label/email);
 *  - форма "Добавить" в конце.
 */
export function ProfilePage() {
  const me = useMe();
  const targetsQ = useKindleTargets();

  return (
    <article className="space-y-6">
      <BackButton />
      <header className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Профиль</h1>
        {me.data ? (
          <p className="text-sm text-muted-foreground">
            {me.data.display_name} ({me.data.email})
          </p>
        ) : null}
      </header>

      {me.data ? <ProfileCard user={me.data} /> : null}

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="flex items-center gap-2 text-base">
            <Mail className="size-4" aria-hidden />
            Kindle-адресаты
          </CardTitle>
          <p className="text-sm text-muted-foreground">
            Адреса для функции «Отправить на Kindle». Можно указать несколько
            (свой, жены, второй планшет). Не забудьте добавить отправителя в
            «Утверждённые отправители» в настройках Amazon.
          </p>
        </CardHeader>
        <CardContent className="space-y-4 pt-2">
          {targetsQ.isLoading ? (
            <Skeleton className="h-12 w-full" />
          ) : targetsQ.error ? (
            <p className="text-sm text-destructive">Не удалось загрузить список.</p>
          ) : (
            <TargetsList targets={targetsQ.data ?? []} />
          )}
          <AddTargetForm existingCount={targetsQ.data?.length ?? 0} />
        </CardContent>
      </Card>
    </article>
  );
}

function TargetsList({ targets }: { targets: KindleTarget[] }) {
  if (targets.length === 0) {
    return (
      <p className="text-sm italic text-muted-foreground">
        Пока ни одного. Добавьте первый адрес ниже.
      </p>
    );
  }
  return (
    <ul className="divide-y divide-border">
      {targets.map((t) => (
        <li key={t.id} className="py-2">
          <TargetRow target={t} />
        </li>
      ))}
    </ul>
  );
}

function TargetRow({ target }: { target: KindleTarget }) {
  const [editing, setEditing] = useState(false);
  const [label, setLabel] = useState(target.label);
  const [email, setEmail] = useState(target.email);
  const update = useUpdateKindleTarget();
  const del = useDeleteKindleTarget();

  if (editing) {
    return (
      <form
        className="flex flex-wrap items-center gap-2"
        onSubmit={async (e) => {
          e.preventDefault();
          try {
            await update.mutateAsync({ id: target.id, label, email });
            setEditing(false);
          } catch {
            /* error выведется через update.error */
          }
        }}
      >
        <Input
          value={label}
          onChange={(e) => setLabel(e.target.value)}
          className="h-9 flex-1 min-w-32"
          placeholder="Название (напр. «Мой Kindle»)"
          aria-label="Название"
        />
        <Input
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          className="h-9 flex-1 min-w-48"
          type="email"
          placeholder="user@kindle.com"
          aria-label="Email"
        />
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
            setLabel(target.label);
            setEmail(target.email);
          }}
        >
          <X className="size-4" aria-hidden />
        </Button>
        {update.error ? (
          <p className="basis-full text-xs text-destructive">
            {messageOf(update.error)}
          </p>
        ) : null}
      </form>
    );
  }

  return (
    <div className="flex flex-wrap items-center gap-2">
      <div className="flex-1 min-w-0">
        <p className="font-medium text-sm">{target.label}</p>
        <p className="text-xs text-muted-foreground truncate">{target.email}</p>
      </div>
      <Button size="sm" variant="ghost" onClick={() => setEditing(true)} aria-label="Изменить">
        <Pencil className="size-4" aria-hidden />
      </Button>
      <Button
        size="sm"
        variant="ghost"
        onClick={() => {
          if (confirm(`Удалить «${target.label}»?`)) {
            del.mutate(target.id);
          }
        }}
        disabled={del.isPending}
        aria-label="Удалить"
      >
        <Trash2 className="size-4 text-destructive" aria-hidden />
      </Button>
    </div>
  );
}

function AddTargetForm({ existingCount }: { existingCount: number }) {
  const [label, setLabel] = useState(existingCount === 0 ? 'Мой Kindle' : '');
  const [email, setEmail] = useState('');
  const add = useAddKindleTarget();
  return (
    <form
      className="space-y-2 border-t border-border pt-4"
      onSubmit={async (e) => {
        e.preventDefault();
        try {
          await add.mutateAsync({ label: label.trim() || 'Kindle', email: email.trim() });
          setLabel('');
          setEmail('');
        } catch {
          /* отрисуем error ниже */
        }
      }}
    >
      <p className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
        Добавить новый
      </p>
      <div className="grid gap-2 sm:grid-cols-2">
        <div className="space-y-1">
          <Label htmlFor="add-label" className="text-xs">Название</Label>
          <Input
            id="add-label"
            value={label}
            onChange={(e) => setLabel(e.target.value)}
            placeholder="напр. «Мой Kindle»"
            className="h-9"
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="add-email" className="text-xs">Email</Label>
          <Input
            id="add-email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            type="email"
            placeholder="user@kindle.com"
            className="h-9"
            required
          />
        </div>
      </div>
      <Button type="submit" size="sm" disabled={add.isPending || !email.trim()}>
        Добавить
      </Button>
      {add.error ? (
        <p className="text-xs text-destructive">{messageOf(add.error)}</p>
      ) : null}
    </form>
  );
}

function messageOf(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 409) return 'Этот email уже добавлен.';
    if (err.status === 400) return 'Похоже, email некорректный.';
    return err.message;
  }
  return 'Не удалось сохранить.';
}

// ── Profile section: display name + password change ─────────────────

/**
 * ProfileCard — секция «Профиль». Над Kindle-адресатами на странице /me.
 * Содержит:
 *   - inline-edit display_name (та же UX-паттерн что у Kindle-target'ов:
 *     pencil → editable input → save/cancel)
 *   - кнопку «Сменить пароль» которая раскрывает форму current+new+confirm
 *
 * Email пока не редактируем — фронт-UX для email change без подтверждения
 * почты рискованный (юзер может опечататься и заблочить себе вход);
 * если потребуется — добавим отдельным шагом с email-verification flow.
 */
function ProfileCard({ user }: { user: User }) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <UserIcon className="size-4" aria-hidden />
          Профиль
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4 pt-2">
        <DisplayNameRow user={user} />
        <PasswordChangeBlock />
      </CardContent>
    </Card>
  );
}

function DisplayNameRow({ user }: { user: User }) {
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(user.display_name);
  const update = useUpdateMe();

  if (editing) {
    return (
      <form
        className="flex flex-wrap items-center gap-2"
        onSubmit={async (e) => {
          e.preventDefault();
          const trimmed = name.trim();
          if (!trimmed) return;
          try {
            await update.mutateAsync({ display_name: trimmed });
            toast.success('Имя обновлено');
            setEditing(false);
          } catch {
            /* выведем error ниже */
          }
        }}
      >
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          className="h-9 flex-1 min-w-48"
          autoFocus
          aria-label="Отображаемое имя"
        />
        <Button type="submit" size="sm" disabled={update.isPending || !name.trim()}>
          <Check className="size-4" aria-hidden />
          Сохранить
        </Button>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          onClick={() => {
            setEditing(false);
            setName(user.display_name);
          }}
        >
          <X className="size-4" aria-hidden />
        </Button>
        {update.error ? (
          <p className="basis-full text-xs text-destructive">{messageOf(update.error)}</p>
        ) : null}
      </form>
    );
  }

  return (
    <div className="flex items-start gap-2">
      <div className="flex-1 min-w-0 space-y-1">
        <p className="text-xs text-muted-foreground uppercase tracking-wider">
          Отображаемое имя
        </p>
        <p className="text-sm font-medium">{user.display_name}</p>
        <p className="text-xs text-muted-foreground">{user.email}</p>
      </div>
      <Button
        size="sm"
        variant="ghost"
        onClick={() => setEditing(true)}
        aria-label="Изменить имя"
      >
        <Pencil className="size-4" aria-hidden />
      </Button>
    </div>
  );
}

/**
 * PasswordChangeBlock — collapsible форма смены пароля. Закрытая
 * по умолчанию (большинству юзеров она не нужна каждый раз),
 * раскрывается клавишей «Сменить пароль».
 *
 * Поля: current_password + new_password + confirm. Confirm
 * сравнивается на клиенте; разные → не submit'им.
 */
function PasswordChangeBlock() {
  const [open, setOpen] = useState(false);
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const change = useChangeMyPassword();

  if (!open) {
    return (
      <Button
        size="sm"
        variant="outline"
        onClick={() => setOpen(true)}
        className="gap-2"
      >
        <KeyRound className="size-4" aria-hidden />
        Сменить пароль
      </Button>
    );
  }

  const mismatch = confirm !== '' && confirm !== next;
  const tooShort = next.length > 0 && next.length < 8;
  const canSubmit =
    !change.isPending && current.length > 0 && next.length >= 8 && next === confirm;

  return (
    <form
      className="space-y-2 rounded-md border border-border p-3"
      onSubmit={async (e) => {
        e.preventDefault();
        if (!canSubmit) return;
        try {
          await change.mutateAsync({ current_password: current, new_password: next });
          toast.success('Пароль обновлён');
          setOpen(false);
          setCurrent('');
          setNext('');
          setConfirm('');
        } catch {
          /* error ниже */
        }
      }}
    >
      <p className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
        Смена пароля
      </p>
      <div className="space-y-2">
        <div className="space-y-1">
          <Label htmlFor="pw-current" className="text-xs">
            Текущий пароль
          </Label>
          <Input
            id="pw-current"
            type="password"
            autoComplete="current-password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
            className="h-9"
            required
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="pw-new" className="text-xs">
            Новый пароль (мин. 8 символов)
          </Label>
          <Input
            id="pw-new"
            type="password"
            autoComplete="new-password"
            value={next}
            onChange={(e) => setNext(e.target.value)}
            className="h-9"
            minLength={8}
            required
          />
          {tooShort ? (
            <p className="text-xs text-destructive">Минимум 8 символов.</p>
          ) : null}
        </div>
        <div className="space-y-1">
          <Label htmlFor="pw-confirm" className="text-xs">
            Повторите новый пароль
          </Label>
          <Input
            id="pw-confirm"
            type="password"
            autoComplete="new-password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            className="h-9"
            required
          />
          {mismatch ? (
            <p className="text-xs text-destructive">Пароли не совпадают.</p>
          ) : null}
        </div>
      </div>
      <div className="flex flex-wrap gap-2 pt-1">
        <Button type="submit" size="sm" disabled={!canSubmit}>
          Обновить
        </Button>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          onClick={() => {
            setOpen(false);
            setCurrent('');
            setNext('');
            setConfirm('');
          }}
        >
          Отмена
        </Button>
      </div>
      {change.error ? (
        <p className="text-xs text-destructive">{passwordChangeMessage(change.error)}</p>
      ) : null}
    </form>
  );
}

function passwordChangeMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 403) return 'Текущий пароль введён неверно.';
    if (err.status === 400) return 'Новый пароль слишком короткий (минимум 8 символов).';
    return err.message;
  }
  return 'Не удалось сменить пароль.';
}
