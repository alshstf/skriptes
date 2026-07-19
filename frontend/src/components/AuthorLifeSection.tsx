import { Link } from '@tanstack/react-router';
import { Landmark } from 'lucide-react';
import { relationPhrase, useWorkLifeEvents } from '@/lib/authorEvents';

/**
 * AuthorLifeSection — «В жизни автора в это время» на карточке книги.
 *
 * Оборотная сторона таймлайна автора: там читатель идёт от биографии к
 * книгам, здесь — от книги к обстоятельствам, в которых она писалась. Секция
 * молчит, пока данных мало (eligible считает бэкенд), — пустой блок хуже
 * отсутствующего.
 */
export function AuthorLifeSection({ workId }: { workId: number }) {
  const { data } = useWorkLifeEvents(workId);
  if (!data?.eligible || data.items.length === 0) return null;

  return (
    <section className="space-y-2">
      <h2 className="flex items-center gap-2 text-sm font-medium text-muted-foreground">
        <Landmark className="size-4" aria-hidden /> В жизни автора в это время
      </h2>
      <ul className="space-y-1">
        {data.items.map((it) => (
          <li key={it.id} className="text-sm">
            <span className="text-muted-foreground">{relationPhrase(it)}: </span>
            {it.title}
            {it.place ? <span className="text-muted-foreground"> · {it.place}</span> : null}
          </li>
        ))}
      </ul>
      {data.author_id ? (
        <Link
          to="/authors/$id"
          params={{ id: String(data.author_id) }}
          className="inline-block text-sm text-muted-foreground underline underline-offset-2 hover:text-foreground"
        >
          Весь таймлайн →
        </Link>
      ) : null}
    </section>
  );
}
