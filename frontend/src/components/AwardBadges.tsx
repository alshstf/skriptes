import { Link } from '@tanstack/react-router';
import { Award } from 'lucide-react';
import { useAuthorAwards, useWorkAwards, yearAnchor, type AwardBadge } from '@/lib/awards';

/**
 * AwardBadges — премии на карточке: «Хьюго 1966» со ссылкой на год премии в
 * разделе «Премии», номинация — в подсказке. Монохромные плашки, как жанры.
 */
export function AwardBadges({ badges }: { badges: AwardBadge[] }) {
  if (badges.length === 0) return null;
  return (
    <ul className="flex flex-wrap gap-1.5" aria-label="Премии">
      {badges.map((b) => (
        <li key={`${b.key}-${b.year}-${b.nomination ?? ''}`}>
          <Link
            to="/awards/$key"
            params={{ key: b.key }}
            hash={yearAnchor(b.year)}
            title={b.nomination ? `${b.name} ${b.year} · ${b.nomination}` : `${b.name} ${b.year}`}
            className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-0.5 text-xs transition hover:bg-accent/40"
          >
            <Award className="size-3.5 text-muted-foreground" aria-hidden />
            <span>{b.name}</span>
            <span className="tabular-nums text-muted-foreground">{b.year}</span>
          </Link>
        </li>
      ))}
    </ul>
  );
}

/** WorkAwards — премии книги (карточка книги). */
export function WorkAwards({ workId }: { workId: number | undefined }) {
  const q = useWorkAwards(workId);
  return <AwardBadges badges={q.data ?? []} />;
}

/** AuthorAwards — премии, врученные автору (карточка автора). */
export function AuthorAwards({ authorId }: { authorId: number }) {
  const q = useAuthorAwards(authorId);
  return <AwardBadges badges={q.data ?? []} />;
}
