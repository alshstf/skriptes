import { Info } from 'lucide-react';
import { Callout } from '@/components/ui/callout';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { AI_FEATURE_LABELS, formatUsd, type AdminAi } from '@/lib/ai';

/**
 * AiSettingsCard — блок «ИИ» на странице «Общее» админки: включено ли, месячный
 * потолок расходов и расход за текущий месяц по функциям.
 *
 * Ключ и модели задаются только в env сервера; без ключа блок — одна подсказка.
 * Выключатель применяется сразу (ui/switch), потолок — поле под общим SaveBar
 * страницы: значение и onChange приходят сверху.
 */
export function AiSettingsCard({
  data,
  budget,
  onBudgetChange,
  budgetInvalid,
  onToggle,
  toggling,
}: {
  data: AdminAi;
  budget: string;
  onBudgetChange: (v: string) => void;
  budgetInvalid: boolean;
  onToggle: (enabled: boolean) => void;
  toggling: boolean;
}) {
  if (!data.configured) {
    return (
      <section className="max-w-2xl space-y-2" aria-labelledby="ai-settings-title">
        <h2 id="ai-settings-title" className="text-lg font-semibold">
          ИИ
        </h2>
        <Callout icon={<Info className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
          ИИ-функции выключены: на сервере не задан ключ (SKRIPTES_LLM_API_KEY). Без него кнопок ИИ нет и наружу ничего
          не уходит. Как включить и что будет отправляться внешней модели — в README, раздел «ИИ».
        </Callout>
      </section>
    );
  }
  const left = Math.max(data.budget_usd - data.spent_usd, 0);
  return (
    <section className="max-w-2xl space-y-4" aria-labelledby="ai-settings-title">
      <div className="space-y-1">
        <h2 id="ai-settings-title" className="text-lg font-semibold">
          ИИ
        </h2>
        <p className="text-sm text-muted-foreground text-pretty">
          Функции читалки на языковой модели. Внешней модели уходят текст прочитанной части книги и вопрос читателя;
          кто спрашивает — не уходит.
        </p>
      </div>

      <div className="flex items-center justify-between gap-4">
        <Label htmlFor="ai-enabled" className="font-normal">
          ИИ-функции включены
        </Label>
        <Switch id="ai-enabled" checked={!data.disabled} disabled={toggling} onCheckedChange={onToggle} />
      </div>

      <div className="space-y-2">
        <Label htmlFor="ai-budget">Потолок расходов в месяц, $</Label>
        <Input
          id="ai-budget"
          className="max-w-40"
          inputMode="decimal"
          value={budget}
          onChange={(e) => onBudgetChange(e.target.value)}
          aria-invalid={budgetInvalid || undefined}
        />
        <p className="text-xs text-muted-foreground text-pretty">
          Когда расход за календарный месяц дойдёт до потолка, ИИ-функции скроются до следующего месяца. От 0 до{' '}
          {data.max_budget_usd}; 0 — ничего не тратить.
        </p>
      </div>

      <div className="space-y-2">
        <p className="text-sm">
          В этом месяце: <span className="font-medium">{formatUsd(data.spent_usd)}</span> из{' '}
          {formatUsd(data.budget_usd)}, осталось {formatUsd(left)}.
        </p>
        {data.by_feature.length > 0 && (
          <table className="w-full max-w-md text-sm">
            <thead className="text-left text-xs text-muted-foreground">
              <tr>
                <th className="py-1 font-normal">Функция</th>
                <th className="py-1 text-right font-normal">Действий</th>
                <th className="py-1 text-right font-normal">Расход</th>
              </tr>
            </thead>
            <tbody>
              {data.by_feature.map((f) => (
                <tr key={f.feature} className="border-t border-border">
                  <td className="py-1">{AI_FEATURE_LABELS[f.feature] ?? f.feature}</td>
                  <td className="py-1 text-right tabular-nums">{f.actions}</td>
                  <td className="py-1 text-right tabular-nums">{formatUsd(f.usd)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <p className="text-xs text-muted-foreground text-pretty">
          Модель: {data.model}; для перевода и пересказов: {data.fast_model}. До {data.daily_per_user} вопросов в день
          на читателя. Модели и суточный предел задаются в env сервера.
        </p>
      </div>
    </section>
  );
}
