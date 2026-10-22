import { Bookmark } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { useToggleToRead } from '@/lib/toRead';
import { cn } from '@/lib/utils';

/**
 * ToReadButton — закладка «Хочу прочитать» на карточке книги (план recsys/03,
 * 3.3). Рядом со ★, но другой смысл: ★ — «люблю», закладка — «ещё не читал,
 * хочу». Монохром: заполненная закладка — foreground (жёлтая только ★). Книга
 * ложится на служебную полку «Хочу прочитать»; Главная напомнит через 60 дней.
 */
export function ToReadButton({
  bookId,
  isToRead,
  variant = 'ghost',
  labelHidden,
  showLabel,
  className,
}: {
  bookId: number;
  isToRead: boolean;
  variant?: 'ghost' | 'outline';
  /** Если true — подпись не показывается никогда (квадратная иконка в мобильном ряду). */
  labelHidden?: boolean;
  /** Если true — подпись и на мобиле (по умолчанию только с sm). */
  showLabel?: boolean;
  className?: string;
}) {
  const toggle = useToggleToRead();
  return (
    <Button
      variant={variant}
      size="sm"
      onClick={() => toggle.mutate({ bookId, next: !isToRead })}
      disabled={toggle.isPending}
      aria-pressed={isToRead}
      aria-label={isToRead ? 'Убрать из «Хочу прочитать»' : 'Добавить в «Хочу прочитать»'}
      title={isToRead ? 'На полке «Хочу прочитать»' : 'Отложить: на полку «Хочу прочитать»'}
      className={cn('gap-1', className)}
    >
      <Bookmark className={cn('size-4', isToRead ? 'fill-foreground' : 'text-muted-foreground')} aria-hidden />
      {labelHidden ? null : <span className={showLabel ? '' : 'hidden sm:inline'}>Хочу прочитать</span>}
    </Button>
  );
}
