import { Link } from '@tanstack/react-router';
import type { RecReason } from '@/lib/recs';
import { reasonParts, reasonText } from '@/lib/recReasons';
import { cn } from '@/lib/utils';

/**
 * RecReasons — строка «почему это здесь» под карточкой ленты: первая причина
 * с текстом (каждая рекомендация объяснима — принцип плана recsys). Ссылки на
 * автора/цикл — внутри карточки-ссылки, поэтому stopPropagation, а клик по ним
 * не ведёт на книгу. Монохром: вторичный текст, без цвета.
 */
export function RecReasons({ reasons, className }: { reasons: RecReason[]; className?: string }) {
  const reason = reasons.find((r) => reasonParts(r).length > 0);
  if (!reason) return null;
  const parts = reasonParts(reason);
  return (
    <p
      className={cn('line-clamp-2 text-xs leading-snug text-muted-foreground text-pretty', className)}
      data-testid="rec-reason"
      aria-label={reasonText(reason)}
    >
      {parts.map((p, i) =>
        p.link ? (
          <Link
            key={i}
            to={p.link.to}
            params={{ id: String(p.link.id) }}
            className="underline-offset-2 hover:underline"
            onClick={(e) => e.stopPropagation()}
          >
            {p.text}
          </Link>
        ) : (
          <span key={i}>{p.text}</span>
        ),
      )}
    </p>
  );
}
