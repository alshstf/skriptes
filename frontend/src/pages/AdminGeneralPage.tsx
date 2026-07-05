import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import { Skeleton } from '@/components/ui/skeleton';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { AdminTabs } from '@/components/AdminTabs';
import { SaveBar } from '@/components/SaveBar';
import { useBrandingSettings, useUpdateBrandingSettings } from '@/lib/admin';
import { DEFAULT_INSTANCE_NAME } from '@/lib/version';
import { ApiError } from '@/lib/api';

const MAX_NAME_LEN = 60;

/**
 * AdminGeneralPage — /admin/general. Общие настройки инстанса. Пока одна —
 * отображаемое имя (заголовок Главной + <title> вкладки). Глобальное, правит
 * админ; читается всеми через /api/version. Применяется сразу при сохранении.
 */
export function AdminGeneralPage() {
  const branding = useBrandingSettings();
  const update = useUpdateBrandingSettings();

  const [name, setName] = useState('');

  useEffect(() => {
    if (branding.data) setName(branding.data.instance_name);
  }, [branding.data]);

  const trimmed = name.trim();
  const dirty = branding.data ? trimmed !== branding.data.instance_name && trimmed !== '' : false;
  const tooLong = trimmed.length > MAX_NAME_LEN;

  const onSave = async () => {
    try {
      const saved = await update.mutateAsync({ instance_name: trimmed });
      setName(saved.instance_name);
      toast.success('Имя инстанса сохранено');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось сохранить');
    }
  };

  const onReset = () => {
    if (branding.data) setName(branding.data.instance_name);
  };

  return (
    <article className="space-y-6">
      <AdminTabs />
      <header className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Общее</h1>
        <p className="text-sm text-muted-foreground">
          Настройки уровня инстанса. Применяются для всех пользователей сервера.
        </p>
      </header>

      {branding.isLoading ? (
        <Skeleton className="h-24 w-full max-w-md" />
      ) : branding.error ? (
        <p className="text-sm text-destructive">Не удалось загрузить настройки.</p>
      ) : (
        <div className="max-w-md space-y-2">
          <Label htmlFor="instance-name">Название инстанса</Label>
          <Input
            id="instance-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={MAX_NAME_LEN}
            placeholder={DEFAULT_INSTANCE_NAME}
            aria-invalid={tooLong || undefined}
          />
          <p className="text-xs text-muted-foreground text-pretty">
            Отображается как заголовок на Главной и в названии вкладки браузера. Пусто — вернётся
            «{DEFAULT_INSTANCE_NAME}».
          </p>
        </div>
      )}

      {dirty && !tooLong && (
        <SaveBar saving={update.isPending} onSave={onSave} onReset={onReset} />
      )}
    </article>
  );
}
