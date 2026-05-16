import { useState } from 'react';
import { cn } from '@/lib/utils';

/**
 * ExpandableText — текст с автоматическим обрезанием по числу строк
 * и кнопкой "Развернуть" / "Свернуть".
 *
 * Использует CSS line-clamp вместо ручного truncate по символам — так
 * не нужно знать точную ширину контейнера, и при reflow граница
 * автоматически пересчитывается. Если текст и так короче `lines` —
 * кнопка не показывается (детектим по scrollHeight в onLoad через ref;
 * см. логику в `setIsClamped`).
 *
 * className применяется к самому <p> — позволяет переопределить
 * typography (размер, цвет).
 */
export function ExpandableText({
  text,
  lines = 4,
  className,
}: {
  text: string;
  lines?: number;
  className?: string;
}) {
  const [expanded, setExpanded] = useState(false);
  // isClamped — флаг "текст реально не влезает в N строк". Определяем
  // в callback ref: измеряем scrollHeight vs clientHeight.
  const [isClamped, setIsClamped] = useState(false);

  return (
    <div className="space-y-1">
      <p
        ref={(el) => {
          if (!el) return;
          // Применяется только в свёрнутом состоянии: scrollHeight >
          // clientHeight означает, что есть невидимый overflow.
          // В развёрнутом всегда показываем "Свернуть", clamp не считаем.
          if (!expanded) {
            setIsClamped(el.scrollHeight > el.clientHeight + 1); // +1 для round-off
          }
        }}
        className={cn(
          'whitespace-pre-line text-sm text-foreground',
          !expanded && `overflow-hidden`,
          className,
        )}
        style={
          expanded
            ? undefined
            : {
                // Tailwind line-clamp-N через arbitrary не всегда подхватывает
                // динамический N, поэтому inline style — надёжнее.
                display: '-webkit-box',
                WebkitLineClamp: lines,
                WebkitBoxOrient: 'vertical',
              }
        }
      >
        {text}
      </p>
      {(isClamped || expanded) ? (
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          className="text-xs font-medium text-primary hover:underline"
        >
          {expanded ? 'Свернуть' : 'Развернуть'}
        </button>
      ) : null}
    </div>
  );
}
