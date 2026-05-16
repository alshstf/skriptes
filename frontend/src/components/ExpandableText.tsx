import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { cn } from '@/lib/utils';

/**
 * ExpandableText — текст с автоматическим обрезанием по числу строк
 * и кнопкой "Развернуть" / "Свернуть".
 *
 * Использует CSS `-webkit-line-clamp` (поддержан во всех современных
 * браузерах). После каждого layout-прохода измеряет фактический
 * scrollHeight vs clientHeight: если текст не помещается в N строк —
 * показываем кнопку. ResizeObserver на родителе пересчитывает при
 * изменении ширины (узкий экран, вернувшийся sidebar).
 *
 * Прошлая версия использовала callback ref — он срабатывал ДО layout,
 * поэтому scrollHeight оказывался = clientHeight, кнопка никогда не
 * появлялась. useLayoutEffect гарантирует измерение после render
 * но до paint.
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
  const [isClamped, setIsClamped] = useState(false);
  const ref = useRef<HTMLParagraphElement>(null);

  // Сбрасываем expanded при смене text — иначе на новом авторе остался
  // бы старый toggle-state.
  useEffect(() => {
    setExpanded(false);
  }, [text]);

  // Измеряем после layout. Делаем в свёрнутом состоянии — в развёрнутом
  // clamp снят, scrollHeight === clientHeight, измерение неинформативно.
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el || expanded) return;

    const measure = () => {
      // +1 на rounding — иначе при идеальном equal иногда даёт false-positive.
      setIsClamped(el.scrollHeight > el.clientHeight + 1);
    };
    measure();

    // Перепроверяем при resize контейнера (mobile rotate, sidebar collapse).
    if (typeof ResizeObserver === 'undefined') return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [text, expanded, lines]);

  return (
    <div className="space-y-1">
      <p
        ref={ref}
        className={cn(
          'whitespace-pre-line text-sm text-foreground',
          !expanded && 'overflow-hidden',
          className,
        )}
        style={
          expanded
            ? undefined
            : {
                display: '-webkit-box',
                WebkitLineClamp: lines,
                WebkitBoxOrient: 'vertical',
              }
        }
      >
        {text}
      </p>
      {isClamped || expanded ? (
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
