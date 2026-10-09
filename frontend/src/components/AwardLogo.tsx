import { useState } from 'react';
import { monogram, type Award } from '@/lib/awards';
import { cn } from '@/lib/utils';

/**
 * AwardLogo — логотип или статуэтка премии (#446) из кэша инстанса
 * (GET /api/awards/{key}/logo). Картинки нет или она не загрузилась —
 * монохромная монограмма из названия, чтобы карточки всё равно различались.
 */
export function AwardLogo({ award, className }: { award: Pick<Award, 'key' | 'name' | 'logo'>; className?: string }) {
  const [failed, setFailed] = useState(false);
  if (award.logo && !failed) {
    // Светлая плашка: логотипы рассчитаны на белый фон, тёмные (Букер, Кларк)
    // в тёмной теме иначе пропадают.
    return (
      <span className={cn('flex items-center justify-center overflow-hidden rounded-md bg-white p-1', className)}>
        <img
          src={`/api/awards/${encodeURIComponent(award.key)}/logo`}
          alt=""
          loading="lazy"
          onError={() => setFailed(true)}
          className="max-h-full max-w-full object-contain"
        />
      </span>
    );
  }
  return (
    <span
      aria-hidden
      className={cn(
        'flex items-center justify-center rounded-md border border-border bg-muted text-xs font-semibold tracking-tight text-muted-foreground',
        className,
      )}
    >
      {monogram(award.name)}
    </span>
  );
}
