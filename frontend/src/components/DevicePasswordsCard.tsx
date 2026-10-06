import { useState } from 'react';
import { Copy, KeyRound, Smartphone, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Callout } from '@/components/ui/callout';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Skeleton } from '@/components/ui/skeleton';
import {
  groupPassword,
  useCreateDevice,
  useDeleteDevice,
  useDevices,
  type CreatedDevice,
  type Device,
} from '@/lib/devices';

const dateFmt = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long', year: 'numeric' });

/**
 * DevicePasswordsCard — пароли устройств (#389): читалка подключается к OPDS
 * (и синхронизации) с таким паролем вместо основного. Пароль показывается
 * один раз — сразу после создания; отзывается по одному.
 */
export function DevicePasswordsCard() {
  const devicesQ = useDevices();
  const [created, setCreated] = useState<CreatedDevice | null>(null);
  const opdsURL = `${window.location.origin}/opds`;

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <Smartphone className="size-4" aria-hidden />
          Пароли устройств
        </CardTitle>
        <p className="text-sm text-pretty text-muted-foreground">
          Для читалок (KOReader, Moon+ Reader, Readest): каталог <code className="text-foreground">{opdsURL}</code>,
          логин — ваш email, пароль — пароль устройства. Основной пароль вне домашней сети читалке не подойдёт.
        </p>
      </CardHeader>
      <CardContent className="space-y-4 pt-2">
        {created ? <NewPassword created={created} onDone={() => setCreated(null)} /> : null}
        {devicesQ.isLoading ? (
          <Skeleton className="h-12 w-full" />
        ) : devicesQ.error ? (
          <p className="text-sm text-destructive">Не удалось загрузить список.</p>
        ) : (
          <ul className="divide-y divide-border rounded-md border border-border empty:hidden">
            {(devicesQ.data ?? []).map((d) => (
              <DeviceRow key={d.id} device={d} />
            ))}
          </ul>
        )}
        <AddDeviceForm onCreated={setCreated} />
      </CardContent>
    </Card>
  );
}

function NewPassword({ created, onDone }: { created: CreatedDevice; onDone: () => void }) {
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(created.password);
      toast.success('Пароль скопирован');
    } catch {
      toast.error('Не удалось скопировать — выделите пароль вручную');
    }
  };
  return (
    <Callout icon={<KeyRound className="size-4 shrink-0" aria-hidden />}>
      <div className="space-y-2">
        <p className="text-pretty">
          Пароль для «{created.device.name}» — запишите его сейчас, больше он показан не будет. Пробелы вводить не нужно.
        </p>
        <div className="flex flex-wrap items-center gap-2">
          <code aria-label="Пароль устройства" className="rounded bg-background px-2 py-1 font-mono text-base tracking-wider text-foreground">
            {groupPassword(created.password)}
          </code>
          <Button size="sm" variant="outline" className="gap-1" onClick={() => void copy()}>
            <Copy className="size-3.5" aria-hidden />
            Копировать
          </Button>
          <Button size="sm" variant="ghost" onClick={onDone}>
            Готово
          </Button>
        </div>
        <p className="text-xs text-muted-foreground">Логин: {created.login}</p>
      </div>
    </Callout>
  );
}

function DeviceRow({ device }: { device: Device }) {
  const del = useDeleteDevice();
  return (
    <li className="flex items-center gap-2 px-3 py-2">
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-medium">{device.name}</p>
        <p className="text-xs text-muted-foreground">
          создан {dateFmt.format(new Date(device.created_at))}
          {' · '}
          {device.last_used_at ? `последний вход ${dateFmt.format(new Date(device.last_used_at))}` : 'ещё не входил'}
        </p>
      </div>
      <Button
        variant="ghost"
        size="icon-sm"
        aria-label={`Отозвать пароль «${device.name}»`}
        disabled={del.isPending}
        onClick={() => {
          if (window.confirm(`Отозвать пароль «${device.name}»? Читалка с ним больше не войдёт.`)) {
            del.mutate(device.id);
          }
        }}
      >
        <Trash2 className="size-4" aria-hidden />
      </Button>
    </li>
  );
}

function AddDeviceForm({ onCreated }: { onCreated: (c: CreatedDevice) => void }) {
  const [name, setName] = useState('');
  const create = useCreateDevice();
  const submit = () => {
    create.mutate(name.trim(), {
      onSuccess: (c) => {
        onCreated(c);
        setName('');
      },
    });
  };
  return (
    <form
      className="flex flex-col gap-2 sm:flex-row"
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
    >
      <Input
        value={name}
        onChange={(e) => setName(e.target.value)}
        placeholder="Устройство, напр. «Kobo в спальне»"
        aria-label="Название устройства"
        maxLength={64}
      />
      <Button type="submit" disabled={create.isPending} className="shrink-0">
        {create.isPending ? 'Создание…' : 'Создать пароль'}
      </Button>
    </form>
  );
}
