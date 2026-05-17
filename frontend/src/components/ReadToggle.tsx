import { Check, CircleDashed } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { useToggleRead } from '@/lib/books';
import { cn } from '@/lib/utils';

/**
 * ReadToggle — кнопка «Прочитано / Не прочитано» на карточке книги.
 *
 * Семантика:
 *  - Это ЕДИНСТВЕННЫЙ источник правды для read-status (плюс auto-mark
 *    из in-browser ридера при дочитывании). Скачать книгу != прочитать —
 *    у пользователей которые читают на Kindle нет иного способа отметить
 *    книгу как прочитанную кроме этой кнопки.
 *  - Optimistic update через useToggleRead: визуально переключается
 *    мгновенно, при ошибке откатываемся.
 *
 * Иконки: галочка для прочитанного, пустой круг для непрочитанного —
 * это конвенция Goodreads / Bookwyrm, должна быть интуитивна.
 */
export function ReadToggle({ bookId, isRead }: { bookId: number; isRead: boolean }) {
  const toggle = useToggleRead();
  const next = !isRead;
  return (
    <Button
      variant={isRead ? 'default' : 'outline'}
      size="sm"
      onClick={() => toggle.mutate({ bookId, isRead: next })}
      disabled={toggle.isPending}
      aria-pressed={isRead}
      aria-label={isRead ? 'Снять отметку «прочитано»' : 'Отметить книгу как прочитанную'}
      className="gap-1"
    >
      {isRead ? (
        <Check className={cn('size-4')} aria-hidden />
      ) : (
        <CircleDashed className={cn('size-4 text-muted-foreground')} aria-hidden />
      )}
      <span className="hidden sm:inline">{isRead ? 'Прочитано' : 'Прочитать'}</span>
    </Button>
  );
}
