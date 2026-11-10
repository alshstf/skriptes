import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import { Skeleton } from '@/components/ui/skeleton';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { AdminTabs } from '@/components/AdminTabs';
import { SaveBar } from '@/components/SaveBar';
import { useBrandingSettings, useSearchMisses, useUpdateBrandingSettings } from '@/lib/admin';
import { DEFAULT_INSTANCE_NAME } from '@/lib/version';
import { ApiError } from '@/lib/api';
import { LoadError } from '@/components/LoadError';
import { SearchMissesCard } from '@/components/SearchMissesCard';
import { AiSettingsCard } from '@/components/AiSettingsCard';
import { parseBudget, useAdminAi, useUpdateAdminAi } from '@/lib/ai';

const MAX_NAME_LEN = 60;

/**
 * AdminGeneralPage — /admin/general. Общие настройки инстанса: отображаемое имя
 * (заголовок Главной + <title> вкладки; читается всеми через /api/version) и
 * блок «ИИ» (потолок расходов, выключатель, расход за месяц). Поля сохраняются
 * общим SaveBar, выключатель ИИ — сразу.
 */
export function AdminGeneralPage() {
  const branding = useBrandingSettings();
  const update = useUpdateBrandingSettings();

  const ai = useAdminAi();
  const updateAi = useUpdateAdminAi();

  const [name, setName] = useState('');
  const [budget, setBudget] = useState('');

  useEffect(() => {
    if (branding.data) setName(branding.data.instance_name);
  }, [branding.data]);
  useEffect(() => {
    if (ai.data) setBudget(String(ai.data.budget_usd));
  }, [ai.data]);

  const trimmed = name.trim();
  const nameDirty = branding.data ? trimmed !== branding.data.instance_name && trimmed !== '' : false;
  const tooLong = trimmed.length > MAX_NAME_LEN;

  const parsedBudget = ai.data ? parseBudget(budget, ai.data.max_budget_usd) : null;
  const budgetDirty = ai.data?.configured ? budget.trim() !== String(ai.data.budget_usd) : false;
  const budgetInvalid = budgetDirty && parsedBudget === null;
  const dirty = nameDirty || budgetDirty;

  const onSave = async () => {
    try {
      if (nameDirty) {
        const saved = await update.mutateAsync({ instance_name: trimmed });
        setName(saved.instance_name);
      }
      if (budgetDirty && ai.data && parsedBudget !== null) {
        await updateAi.mutateAsync({
          monthly_budget_usd: parsedBudget,
          disabled: ai.data.disabled,
          disabled_users: ai.data.disabled_users,
        });
      }
      toast.success('Настройки сохранены');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось сохранить');
    }
  };

  const onReset = () => {
    if (branding.data) setName(branding.data.instance_name);
    if (ai.data) setBudget(String(ai.data.budget_usd));
  };

  // Выключатель ИИ — сразу, без SaveBar; несохранённый потолок при этом не трогаем.
  const onToggleAi = async (enabled: boolean) => {
    if (!ai.data) return;
    try {
      await updateAi.mutateAsync({
        monthly_budget_usd: ai.data.budget_usd,
        disabled: !enabled,
        disabled_users: ai.data.disabled_users,
      });
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось сохранить');
    }
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
        <LoadError error={branding.error}>Не удалось загрузить настройки.</LoadError>
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

      {ai.data && (
        <AiSettingsCard
          data={ai.data}
          budget={budget}
          onBudgetChange={setBudget}
          budgetInvalid={budgetInvalid}
          onToggle={onToggleAi}
          toggling={updateAi.isPending}
        />
      )}

      {dirty && !tooLong && !budgetInvalid && (
        <SaveBar saving={update.isPending || updateAi.isPending} onSave={onSave} onReset={onReset} />
      )}

      <SearchMissesCard query={useSearchMisses(90)} />
    </article>
  );
}
