import { useEffect, useState } from 'react';
import { BookOpen } from 'lucide-react';
import { cn } from '@/lib/utils';

/**
 * BookCover — обложка книги с плейсхолдером.
 *
 * Источник картинки: либо `src` (готовый URL, напр. on-demand
 * `/api/covers/book/{id}`), либо `coverPath` (content-addressable имя →
 * `/api/covers/{coverPath}`). Если картинка не загрузилась (404 — у книги
 * нет обложки, или файл вытеснен из кэша) → `onError` переключает на
 * плейсхолдер. Так список с on-demand-обложками не показывает «битую
 * картинку»: что есть — грузится, чего нет — плейсхолдер.
 *
 * Два вида плейсхолдера (`placeholder`):
 *  - 'icon' (дефолт) — иконка + название (для крупной обложки на карточке);
 *  - 'monogram' — компактный цветной тайл с первой буквой (для thumbnail
 *    в списках).
 *
 * `aspect-[2/3]` — типичная пропорция книжной обложки; ширина — через
 * className родителя.
 */
export function BookCover({
  coverPath,
  src,
  title,
  className,
  placeholder = 'icon',
}: {
  coverPath?: string;
  src?: string;
  title: string;
  className?: string;
  placeholder?: 'icon' | 'monogram';
}) {
  const url = src ?? (coverPath ? `/api/covers/${coverPath}` : undefined);
  const [failed, setFailed] = useState(false);
  // Сброс флага ошибки при смене URL (напр. при пагинации/смене книги).
  useEffect(() => {
    setFailed(false);
  }, [url]);

  const base = cn(
    'aspect-[2/3] rounded-md border border-border bg-muted shadow-sm overflow-hidden shrink-0 self-start',
    className,
  );

  if (url && !failed) {
    return (
      <img
        src={url}
        alt={`Обложка: ${title}`}
        className={cn(base, 'object-cover')}
        loading="lazy"
        onError={() => setFailed(true)}
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
