import { useState } from 'react';
import { Star } from 'lucide-react';
import { cn } from '@/lib/utils';

/**
 * RatingStars — 5-звёздный контрол пользовательской оценки.
 *
 * Монохром (правило №9): выбранные звёзды `fill-foreground`, пустые —
 * `text-muted-foreground`. Жёлтая ★ зарезервирована за избранным — здесь не она.
 *
 * Интерактивный режим: hover-превью, клик ставит оценку, клик по текущей —
 * снимает (onChange(null)). readOnly — только показ (средняя/чужая оценка).
 */
export function RatingStars({
  value,
  onChange,
  readOnly = false,
  disabled = false,
  size = 'md',
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
  const px = size === 'sm' ? 'size-4' : 'size-6';
  const star = (active: boolean) => (
    <Star
      className={cn(px, active ? 'fill-foreground stroke-foreground' : 'text-muted-foreground')}
      aria-hidden
    />
  );

  if (readOnly || !onChange) {
    return (
      <span className="inline-flex items-center gap-0.5" aria-label={`Оценка ${value} из 5`}>
        {[1, 2, 3, 4, 5].map((n) => (
          <span key={n}>{star(n <= value)}</span>
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
          {star(n <= shown)}
        </button>
      ))}
    </span>
  );
}
