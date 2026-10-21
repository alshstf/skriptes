import { Link } from '@tanstack/react-router';
import type { RecReason } from '@/lib/recs';
import { reasonParts, reasonText } from '@/lib/recReasons';
import { cn } from '@/lib/utils';

/**
 * RecReasons — строка «почему это здесь» под карточкой ленты: первая причина
 * с текстом (каждая рекомендация объяснима — принцип плана recsys). Ссылки на
 * автора/цикл — внутри карточки-ссылки, поэтому stopPropagation, а клик по ним
 * не ведёт на книгу. Монохром: вторичный текст, без цвета. До трёх строк xs:
 * карточка узкая (w-28/32), а причина с основанием («дочитали «X» · № 3 в
 * цикле Y») в две строки не влезает — третья лучше, чем обрезанная ссылка.
 */
export function RecReasons({ reasons, className }: { reasons: RecReason[]; className?: string }) {
  const reason = reasons.find((r) => reasonParts(r).length > 0);
  if (!reason) return null;
  const parts = reasonParts(reason);
  return (
    <p
      className={cn('line-clamp-3 text-xs leading-snug text-muted-foreground text-pretty', className)}
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
