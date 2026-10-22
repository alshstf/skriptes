import { useState } from 'react';
import { RATING_REASONS, reasonLabel, useSetRatingReasons, type RatingReason } from '@/lib/ratings';
import { cn } from '@/lib/utils';

/**
 * RatingReasonsPrompt — второй шаг после низкой оценки (план recsys/03, 3.1):
 * «Почему?» и чипы-причины (мультивыбор), «Сохранить» / «Пропустить». Причина
 * учит модель быстрее звёзд, поэтому спрашиваем сразу при оценке 1–2, но один
 * тап «Пропустить» — и больше не пристаём (на карточке остаётся ссылка
 * «почему?» для 1–3). Монохром: выбранный чип — инверсия (как чипы минут).
 */
export function RatingReasonsPrompt({
  workId,
  cardKey,
  initial = [],
  onDone,
  compact = false,
}: {
  workId: number;
  /** Кэш открытой карточки — патчим user_rating_reasons. */
  cardKey?: readonly (string | number)[];
  initial?: RatingReason[];
  /** Сохранили или пропустили — родитель прячет шаг. */
  onDone: () => void;
  compact?: boolean;
}) {
  const [selected, setSelected] = useState<Set<RatingReason>>(() => new Set(initial));
  const save = useSetRatingReasons(workId, cardKey);
  const toggle = (k: RatingReason) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(k)) next.delete(k);
      else next.add(k);
      return next;
    });
  return (
    <div className={cn('space-y-2', compact ? 'text-xs' : 'text-sm')} data-testid="rating-reasons">
      <p className="text-pretty text-muted-foreground">Почему? Так подбор будет точнее.</p>
      <div className="flex flex-wrap gap-1.5" role="group" aria-label="Причины низкой оценки">
        {RATING_REASONS.map((r) => (
          <button
            key={r.key}
            type="button"
            aria-pressed={selected.has(r.key)}
            onClick={() => toggle(r.key)}
            className={cn(
              'rounded-full border px-2.5 py-0.5 text-xs transition-colors',
              selected.has(r.key)
                ? 'border-foreground bg-foreground text-background'
                : 'border-border text-muted-foreground hover:bg-accent hover:text-foreground',
            )}
          >
            {r.label}
          </button>
        ))}
      </div>
      <div className="flex items-center gap-3">
        <button
          type="button"
          disabled={save.isPending}
          onClick={() => save.mutate([...selected], { onSuccess: onDone })}
          className="rounded-md border border-border px-2.5 py-1 text-xs transition-colors hover:bg-accent disabled:opacity-50"
        >
          Сохранить
        </button>
        <button
          type="button"
          onClick={onDone}
          className="text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
        >
          Пропустить
        </button>
      </div>
    </div>
  );
}

/**
 * RatingReasonsLine — что уже сказано о низкой оценке: «Почему: скучно · долго ·
 * изменить», а без причин — ссылка «почему?» (оценка 1–3, спросить по желанию).
 */
export function RatingReasonsLine({ reasons, onEdit }: { reasons: RatingReason[]; onEdit: () => void }) {
  if (reasons.length === 0) {
    return (
      <button
        type="button"
        onClick={onEdit}
        className="text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
        data-testid="rating-reasons-ask"
      >
        почему?
      </button>
    );
  }
  return (
    <p className="text-xs text-muted-foreground" data-testid="rating-reasons-line">
      Почему: {reasons.map(reasonLabel).join(' · ')}
      {' · '}
      <button type="button" onClick={onEdit} className="underline-offset-2 hover:text-foreground hover:underline">
        изменить
      </button>
    </p>
  );
}
