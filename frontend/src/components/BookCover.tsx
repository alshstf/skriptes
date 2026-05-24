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
 *     плейсхолдер.
 *
 * Два вида плейсхолдера (`placeholder`):
 *  - 'icon' (дефолт) — иконка + название книги. Для больших обложек
 *    (карточка книги), где обложка вот-вот подтянется через polling.
 *  - 'monogram' — компактный цветной тайл с первой буквой названия.
 *    Для маленьких thumbnail'ов в списках: пустая иконка там «отъедает
 *    место», а монограм выглядит как осознанный аватар. Цвет
 *    детерминирован по названию.
 *
 * `aspect-[2/3]` — типичная пропорция книжной обложки. Width задаётся
 * через className родителем.
 */
export function BookCover({
  coverPath,
  title,
  className,
  placeholder = 'icon',
}: {
  coverPath?: string;
  title: string;
  className?: string;
  placeholder?: 'icon' | 'monogram';
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
  if (placeholder === 'monogram') {
    const letter = title.trim().charAt(0).toUpperCase() || '?';
    return (
      <div
        className={cn(base, 'flex items-center justify-center font-semibold text-white')}
        style={{ backgroundColor: monogramColor(title) }}
        role="img"
        aria-label={`Обложка: ${title}`}
      >
        <span className="text-lg sm:text-xl">{letter}</span>
      </div>
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

// monogramColor — детерминированный приглушённый цвет фона по названию.
// HSL с фиксированными S/L (читаемо с белой буквой и в тёмной теме),
// варьируем только hue по простому хешу строки.
function monogramColor(title: string): string {
  let hash = 0;
  for (let i = 0; i < title.length; i++) {
    hash = (hash * 31 + title.charCodeAt(i)) % 360;
  }
  return `hsl(${hash} 42% 32%)`;
}
