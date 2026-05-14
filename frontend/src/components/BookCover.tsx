import { BookOpen } from 'lucide-react';
import { cn } from '@/lib/utils';

/**
 * BookCover — обложка книги с плейсхолдером.
 *
 * Решает две UX-проблемы lazy-enrichment'а:
 *  1. Layout-сдвиг: и плейсхолдер, и реальная картинка занимают
 *     ОДИН и тот же размер (fixed width + aspect-[2/3]) — при подмене
 *     карточка не "прыгает".
 *  2. Пустое место без обратной связи: пока обложки нет, рисуем
 *     приятный плейсхолдер с иконкой и названием книги в углу.
 *
 * Когда `cover_path` приходит в ответе useBook (через polling), компонент
 * естественно перерисовывается через React — без перезагрузки страницы.
 *
 * `aspect-[2/3]` — типичная пропорция книжной обложки. Width задаётся
 * через className родителем, чтобы можно было использовать разный
 * размер на разных страницах.
 */
export function BookCover({
  coverPath,
  title,
  className,
}: {
  coverPath?: string;
  title: string;
  className?: string;
}) {
  const base = cn(
    'aspect-[2/3] rounded-md border border-border bg-muted shadow-sm overflow-hidden shrink-0 self-start',
    className,
  );
  if (coverPath) {
    return (
      <img
        src={`/api/covers/${coverPath}`}
        alt={`Обложка: ${title}`}
        className={cn(base, 'object-cover')}
        loading="lazy"
      />
    );
  }
  return (
    <div
      className={cn(base, 'flex flex-col items-center justify-center gap-2 p-3 text-muted-foreground')}
      role="img"
      aria-label={`Обложка: ${title} (загружается)`}
    >
      <BookOpen className="size-8 opacity-40" aria-hidden />
      <span className="text-xs line-clamp-3 text-center">{title}</span>
    </div>
  );
}
