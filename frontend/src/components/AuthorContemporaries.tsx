import { Link } from '@tanstack/react-router';
import { Fragment } from 'react';
import { useAuthorContemporaries, type Contemporary } from '@/lib/catalog';

/**
 * AuthorContemporaries — «Современники: Гоголь Николай, Дюма Александр, …» на
 * карточке автора (#505): известные авторы библиотеки, чья взрослая жизнь
 * пересекалась с его жизнью (считает бэкенд, только по годам жизни). Годы — в
 * подсказке ссылки. Нет годов жизни или некого показать — строки нет.
 */
export function AuthorContemporaries({ authorId }: { authorId: number }) {
  const { data } = useAuthorContemporaries(authorId);
  if (!data?.length) return null;
  return (
    <p className="text-sm text-muted-foreground text-pretty" data-testid="author-contemporaries">
      Современники:{' '}
      {data.map((c, i) => (
        <Fragment key={c.id}>
          {i > 0 ? ', ' : null}
          <Link
            to="/authors/$id"
            params={{ id: String(c.id) }}
            title={lifeYears(c)}
            className="text-foreground underline-offset-2 hover:underline"
          >
            {[c.last_name, c.first_name].filter(Boolean).join(' ')}
          </Link>
        </Fragment>
      ))}
    </p>
  );
}

function lifeYears(c: Contemporary): string {
  return c.died_year ? `${c.born_year}–${c.died_year}` : `род. ${c.born_year}`;
}
