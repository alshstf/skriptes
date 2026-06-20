import { useState } from 'react';
import { Circle } from 'lucide-react';
import { cn } from '@/lib/utils';

/**
 * RatingControl — контрол пользовательской оценки (5 кружков, шкала 1–5).
 *
 * Намеренно НЕ звезда: звезда занята избранным (жёлтая ★) и встречается часто —
 * оценка должна читаться как отдельная сущность. Монохром (правило №9): выбранные
 * кружки — `fill-foreground`, пустые — кольцо `muted`.
 *
 * Интерактив: hover-превью, клик ставит оценку, клик по текущей — снимает
 * (onChange(null)). readOnly (или без onChange) — только показ (средняя/чужая).
 */
export function RatingControl({
  value,
  onChange,
  readOnly = false,
  disabled = false,
  size = 'sm',
}: {
  /** Текущая оценка 0–5 (0 = нет оценки). */
  value: number;
  /** Клик: n (поставить) или null (снять, при клике по текущей). */
  onChange?: (next: number | null) => void;
  readOnly?: boolean;
  disabled?: boolean;
  size?: 'sm' | 'md';
}) {
  const [hover, setHover] = useState(0);
  const shown = hover || value;
  const px = size === 'sm' ? 'size-3.5' : 'size-5';
  const dot = (active: boolean) => (
    <Circle
      className={cn(px, active ? 'fill-foreground stroke-foreground' : 'text-muted-foreground')}
      aria-hidden
    />
  );

  if (readOnly || !onChange) {
    return (
      <span className="inline-flex items-center gap-1" aria-label={`Оценка ${value} из 5`}>
        {[1, 2, 3, 4, 5].map((n) => (
          <span key={n}>{dot(n <= value)}</span>
        ))}
      </span>
    );
  }

  return (
    <span className="inline-flex items-center gap-0.5" role="group" aria-label="Ваша оценка">
      {[1, 2, 3, 4, 5].map((n) => (
        <button
          key={n}
          type="button"
          disabled={disabled}
          onMouseEnter={() => setHover(n)}
          onMouseLeave={() => setHover(0)}
          onFocus={() => setHover(n)}
          onBlur={() => setHover(0)}
          onClick={() => onChange(n === value ? null : n)}
          aria-label={n === value ? `Снять оценку (сейчас ${n})` : `Оценить на ${n}`}
          aria-pressed={n <= value}
          className="rounded p-0.5 transition hover:scale-110 disabled:opacity-50"
        >
          {dot(n <= shown)}
        </button>
      ))}
    </span>
  );
}
