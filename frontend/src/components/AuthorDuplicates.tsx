import { Link } from '@tanstack/react-router';
import { Users } from 'lucide-react';
import { Callout } from '@/components/ui/callout';
import { MergeAuthorsDialog } from '@/components/MergeAuthorsDialog';
import { useAuthorDuplicates, type DuplicateAuthor } from '@/lib/admin';
import { useMe } from '@/lib/auth';
import { pluralBooks } from '@/lib/format';
import type { Author } from '@/lib/catalog';

const reasonText: Record<DuplicateAuthor['reason'], string> = {
  middle_name: 'та же фамилия и имя',
  latin_name: 'то же латинское имя в переводах',
};

/**
 * AuthorDuplicates — подсказка админу на карточке автора (#308): возможно, тот
 * же человек записан ещё раз («Лукьяненко Сергей» и «Лукьяненко Сергей
 * Васильевич»). Бывают и разные люди с теми же ФИ — поэтому годы и число книг
 * рядом, а слияние только вручную. Не-админ не видит ничего.
 */
export function AuthorDuplicates({ author }: { author: Pick<Author, 'id' | 'full_name' | 'book_count'> }) {
  const { data: me } = useMe();
  const isAdmin = me?.role === 'admin';
  const { data: dups } = useAuthorDuplicates(author.id, isAdmin);
  if (!isAdmin || !dups || dups.length === 0) return null;
  return (
    <Callout icon={<Users className="size-4" aria-hidden />}>
      <div className="space-y-2">
        <p className="font-medium text-foreground">Возможно, это тот же автор</p>
        <ul className="space-y-2">
          {dups.map((d) => (
            <li key={d.id} className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <Link to="/authors/$id" params={{ id: String(d.id) }} className="font-medium text-foreground hover:underline">
                {d.full_name}
              </Link>
              <span className="tabular-nums">
                · {d.book_count} {pluralBooks(d.book_count)}
                {d.years_active ? ` · ${d.years_active.from}–${d.years_active.to}` : ''} · {reasonText[d.reason]}
              </span>
              <MergeAuthorsDialog
                a={{ id: author.id, full_name: author.full_name, book_count: author.book_count }}
                b={d}
                navigateToKept
              />
            </li>
          ))}
        </ul>
      </div>
    </Callout>
  );
}
