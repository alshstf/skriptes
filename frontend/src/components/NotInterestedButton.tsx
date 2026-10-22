import { Ban } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { useToggleBlock, type BlockTarget } from '@/lib/blocklist';
import { cn } from '@/lib/utils';

/**
 * NotInterestedButton — «не предлагать» автора или цикл (план recsys/03, 3.2)
 * рядом с колокольчиком подписки. Слова нарочно «не предлагать», а не «скрыть»:
 * скрытие на инстансе (админ, InstanceHideControl) — другое. Здесь каталог и
 * поиск не меняются, только лента рекомендаций и новинки по подпискам; список
 * с отменой — в профиле.
 */
export function NotInterestedButton({
  target,
  id,
  isBlocked,
  className,
}: {
  target: BlockTarget;
  id: number;
  isBlocked: boolean;
  className?: string;
}) {
  const toggle = useToggleBlock();
  const noun = target === 'author' ? 'автора' : 'цикл';
  return (
    <Button
      variant="ghost"
      size="sm"
      onClick={() => toggle.mutate({ target, id, next: !isBlocked })}
      disabled={toggle.isPending}
      aria-pressed={isBlocked}
      aria-label={isBlocked ? `Снова предлагать ${noun}` : `Не предлагать ${noun}`}
      title={
        isBlocked
          ? 'В рекомендациях не предлагается — нажмите, чтобы вернуть'
          : 'Не предлагать в рекомендациях и новинках по подпискам (в каталоге и поиске остаётся)'
      }
      className={cn('gap-1', className)}
    >
      <Ban className={cn('size-4', isBlocked ? 'text-foreground' : 'text-muted-foreground')} aria-hidden />
      <span className="hidden sm:inline">{isBlocked ? 'Не предлагаем' : 'Не предлагать'}</span>
    </Button>
  );
}
