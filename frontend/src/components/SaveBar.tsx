import { Button } from '@/components/ui/button';

/**
 * SaveBar — закреплённая внизу панель сохранения. Рендерить только при
 * наличии несохранённых изменений (страница сама решает): тогда кнопки
 * всегда на виду, даже если форма длиннее экрана. `sticky bottom-0`
 * прилипает к низу вьюпорта, пока контент выше скроллится.
 *
 * Общий контекстозависимый паттерн «Сохранить» для разделов настроек
 * (Контент, Лимиты кэша и т.п.).
 */
export function SaveBar({
  saving,
  onSave,
  onReset,
  canSave = true,
}: {
  saving: boolean;
  onSave: () => void;
  onReset: () => void;
  // false блокирует «Сохранить» (например, невалидные значения), но бар
  // всё равно показан — чтобы можно было «Отменить» или поправить.
  canSave?: boolean;
}) {
  return (
    <div className="sticky bottom-0 z-20 flex items-center justify-between gap-3 rounded-lg border border-border bg-background/95 px-4 py-3 shadow-lg backdrop-blur supports-[backdrop-filter]:bg-background/80">
      <span className="text-sm text-muted-foreground">Есть несохранённые изменения</span>
      <div className="flex shrink-0 gap-2">
        <Button variant="ghost" size="sm" onClick={onReset} disabled={saving}>
          Отменить
        </Button>
        <Button size="sm" onClick={onSave} disabled={saving || !canSave}>
          {saving ? 'Сохранение…' : 'Сохранить'}
        </Button>
      </div>
    </div>
  );
}
