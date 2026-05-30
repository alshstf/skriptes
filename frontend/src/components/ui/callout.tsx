import type { ReactNode } from 'react';

import { cn } from '@/lib/utils';

/**
 * Callout — компактная инфо-панель (callout / notice). Монохромная под нашу
 * чёрно-белую тему: рамка + bg-muted + приглушённый текст, акцент — иконкой,
 * а не цветом. Для подсказок/предупреждений в формах настроек.
 *
 * icon — необязательная иконка (caller задаёт размер, напр. size-3.5).
 */
export function Callout({
  icon,
  children,
  className,
}: {
  icon?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        'flex items-start gap-2 rounded-md border border-border bg-muted/50 px-3 py-2 text-xs text-pretty text-muted-foreground',
        className,
      )}
    >
      {icon}
      <div className="min-w-0 flex-1">{children}</div>
    </div>
  );
}
