import { Link } from '@tanstack/react-router';
import { Bookmark, Highlighter, NotebookPen } from 'lucide-react';
import { placeLabel, useWorkAnnotations } from '@/lib/annotations';

/**
 * MyNotes — «Мои заметки» на карточке (#389): закладки и выделения пользователя
 * во всех изданиях работы; каждая открывает ридер на своём месте.
 */
export function MyNotes({ workId }: { workId: number | undefined }) {
  const q = useWorkAnnotations(workId);
  const items = q.data ?? [];
  if (items.length === 0) return null;
  const editions = new Set(items.map((a) => a.book_id));
  return (
    <section className="space-y-2" aria-labelledby="my-notes-title">
      <h3 id="my-notes-title" className="flex items-center gap-2 text-sm font-medium">
        <NotebookPen className="size-4" aria-hidden />
        Мои заметки
        <span className="text-xs font-normal text-muted-foreground tabular-nums">{items.length}</span>
      </h3>
      <ul className="space-y-2 text-sm">
        {items.map((a) => (
          <li key={a.id}>
            <Link
              to="/books/$id/read"
              params={{ id: String(a.book_id) }}
              search={{ cfi: a.cfi }}
              className="block space-y-0.5 rounded-md p-2 transition hover:bg-accent/40"
            >
              <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
                {a.kind === 'bookmark' ? (
                  <Bookmark className="size-3.5 shrink-0" aria-hidden />
                ) : (
                  <Highlighter className="size-3.5 shrink-0" aria-hidden />
                )}
                {a.kind === 'bookmark' ? 'Закладка' : 'Выделение'}
                {placeLabel(a) ? ` · ${placeLabel(a)}` : ''}
                {editions.size > 1 && a.edition_lang ? ` · ${a.edition_title} (${a.edition_lang})` : ''}
              </span>
              {a.excerpt ? <span className="line-clamp-3 block text-pretty">«{a.excerpt}»</span> : null}
              {a.note ? <span className="block text-pretty text-muted-foreground">{a.note}</span> : null}
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}
