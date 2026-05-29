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

// coverOutcome — модульный кэш исхода загрузки обложки по URL
// (true = загрузилась, false = 404/ошибка). Переживает ре-маунты
// виртуализированных строк списка: при повторном появлении книги в окне
// обложка не «моргает» (placeholder ⇄ картинка), а для книг без обложки не
// шлётся повторный 404-запрос — сразу рисуется монограмма. Размер ограничен
// числом реально просмотренных книг за сессию (по строке-URL на книгу).
const coverOutcome = new Map<string, boolean>();

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
  // Инициализируем из кэша исхода: если по этому url уже был 404 — сразу
  // плейсхолдер, без повторного запроса и мелькания при ре-маунте.
  const [failed, setFailed] = useState(() => url != null && coverOutcome.get(url) === false);
  // При смене URL синхронизируемся с кэшем (а не сбрасываем в false: иначе
  // при возврате 404-обложки в окно снова мелькал бы запрос → плейсхолдер).
  useEffect(() => {
    setFailed(url != null && coverOutcome.get(url) === false);
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
        decoding="async"
        onLoad={() => coverOutcome.set(url, true)}
        onError={() => {
          coverOutcome.set(url, false);
          setFailed(true);
        }}
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
