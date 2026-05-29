import { toast } from 'sonner';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { ProfileTabs } from '@/components/ProfileTabs';
import { cn } from '@/lib/utils';
import {
  useAppearance,
  useUpdateAppearance,
  genreChipClass,
  type GenreChipStyle,
} from '@/lib/appearance';
import { ApiError } from '@/lib/api';

/**
 * ProfileAppearancePage — /me/appearance. Персональные настройки внешнего
 * вида (синхронно между устройствами + мгновенно из localStorage). Пока
 * один параметр — стиль жанровых меток; раздел рассчитан на рост.
 *
 * Сохранение — сразу при выборе (один параметр, save-бар не нужен); чипы во
 * всём приложении меняются мгновенно (оптимистичная запись в localStorage).
 */
const OPTIONS: { value: GenreChipStyle; label: string; hint: string }[] = [
  {
    value: 'soft',
    label: 'Приглушённые',
    hint: 'Тихие метки — не отвлекают от названия и автора (по умолчанию).',
  },
  {
    value: 'classic',
    label: 'Контрастные',
    hint: 'Заметные плашки с заливкой — как было раньше.',
  },
];

const SAMPLE = ['Фантастика', 'Детектив', 'Приключения'];

export function ProfileAppearancePage() {
  const appearance = useAppearance();
  const update = useUpdateAppearance();
  const current = appearance.data?.genre_chip_style ?? 'soft';

  const choose = async (value: GenreChipStyle) => {
    if (value === current) return;
    try {
      await update.mutateAsync({ genre_chip_style: value });
      toast.success('Внешний вид обновлён');
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : 'Не удалось сохранить');
    }
  };

  return (
    <article className="space-y-6">
      <ProfileTabs />
      <header className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Внешний вид</h1>
        <p className="text-sm text-muted-foreground">
          Оформление интерфейса. Действует только для вас и синхронизируется между устройствами.
        </p>
      </header>

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-base">Жанровые метки в списках</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2 pt-2 sm:max-w-md">
          {OPTIONS.map((o) => {
            const selected = current === o.value;
            return (
              <button
                key={o.value}
                type="button"
                onClick={() => void choose(o.value)}
                disabled={update.isPending}
                aria-pressed={selected}
                className={cn(
                  'flex w-full items-start gap-3 rounded-lg border p-3 text-left transition disabled:opacity-70',
                  selected ? 'border-primary bg-accent/40' : 'border-border hover:bg-accent/30',
                )}
              >
                <span
                  aria-hidden
                  className={cn(
                    'mt-0.5 size-4 shrink-0 rounded-full border',
                    selected ? 'border-primary bg-primary' : 'border-input',
                  )}
                />
                <span className="min-w-0 flex-1 space-y-1.5">
                  <span className="block text-sm font-medium">{o.label}</span>
                  <span className="block text-xs text-muted-foreground">{o.hint}</span>
                  <span className="flex flex-wrap gap-1 pt-0.5">
                    {SAMPLE.map((s) => (
                      <Badge key={s} variant="secondary" className={genreChipClass(o.value)}>
                        {s}
                      </Badge>
                    ))}
                  </span>
                </span>
              </button>
            );
          })}
        </CardContent>
      </Card>
    </article>
  );
}
