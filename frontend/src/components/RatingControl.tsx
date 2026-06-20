import { useState } from 'react';
import { cn } from '@/lib/utils';

/**
 * RatingControl — пользовательская оценка как сегментированная числовая шкала
 * 1–5 (цифры в ячейках). Намеренно НЕ звезда (звезда занята избранным) и не
 * «налив» из 5 одинаковых глифов — явная числовая шкала: подсвечена ВЫБРАННАЯ
 * цифра. Монохром (правило №9): выбранная — инверсия (`bg-foreground`), прочие —
 * рамка + muted-цифра.
 *
 * Интерактив: hover-превью, клик ставит оценку, клик по текущей — снимает
 * (onChange(null)). readOnly (или без onChange) — только показ.
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
  const box = size === 'sm' ? 'size-5 text-xs' : 'size-7 text-sm';
  const cell = 'inline-flex items-center justify-center rounded font-medium tabular-nums';

  if (readOnly || !onChange) {
    return (
      <span className="inline-flex items-center gap-1" aria-label={`Оценка ${value} из 5`}>
        {[1, 2, 3, 4, 5].map((n) => (
          <span
            key={n}
            className={cn(
              cell,
              box,
              n === value ? 'bg-foreground text-background' : 'border border-border text-muted-foreground',
            )}
          >
            {n}
          </span>
        ))}
      </span>
    );
  }

  return (
    <span className="inline-flex items-center gap-1" role="group" aria-label="Ваша оценка">
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
          aria-pressed={n === value}
          className={cn(
            cell,
            box,
            'transition disabled:opacity-50',
            n === shown
              ? 'bg-foreground text-background'
              : 'border border-border text-muted-foreground hover:border-foreground',
          )}
        >
          {n}
        </button>
      ))}
    </span>
  );
}
