import { useState } from 'react';
import { Mail, Trash2, Pencil, Check, X } from 'lucide-react';
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
import { useMe } from '@/lib/auth';
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
