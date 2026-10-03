import { Link } from '@tanstack/react-router';
import { AdminTabs } from '@/components/AdminTabs';
import { MergeAuthorsDialog } from '@/components/MergeAuthorsDialog';
import { Button } from '@/components/ui/button';
import { Callout } from '@/components/ui/callout';
import { Skeleton } from '@/components/ui/skeleton';
import { useDuplicatePairs, type DuplicateAuthor } from '@/lib/admin';
import { pluralBooks, pluralRu } from '@/lib/format';

/**
 * AdminAuthorDuplicatesPage — /admin/authors: пары возможных дублей авторов
 * (#308) от самых известных — «Фамилия Имя» и «Фамилия Имя Отчество», одно и то
 * же латинское имя в переводах. Бывают и разные люди с теми же ФИ (Матесон
 * Ричард и Ричард Кристиан) — поэтому годы и число книг рядом, а слияние —
 * вручную, по одной паре.
 */
export function AdminAuthorDuplicatesPage() {
  const { data, isLoading, error, hasNextPage, fetchNextPage, isFetchingNextPage } = useDuplicatePairs();
  const pairs = data?.pages.flatMap((p) => p.items) ?? [];
  const total = data?.pages[0]?.total ?? 0;

  return (
    <article className="space-y-6">
      <AdminTabs />
      <header className="space-y-1">
        <h1 className="text-2xl font-semibold">Дубли авторов</h1>
        <p className="text-sm text-muted-foreground text-pretty">
          Возможно, один человек записан дважды: «Фамилия Имя» и «Фамилия Имя Отчество» или одно и то же латинское
          имя в переводах. Сверху — самые известные. Бывают и разные люди с теми же именем и фамилией — сверьте годы
          и книги.
        </p>
      </header>

      {error ? (
        <p role="alert" className="text-sm text-destructive">
          Не удалось загрузить: {(error as Error).message}
        </p>
      ) : null}

      {isLoading ? (
        <div className="space-y-2">
          {Array.from({ length: 6 }, (_, i) => (
            <Skeleton key={i} className="h-14 w-full" />
          ))}
        </div>
      ) : pairs.length === 0 ? (
        <Callout>Возможных дублей не найдено.</Callout>
      ) : (
        <>
          <p className="text-sm text-muted-foreground tabular-nums">
            {total} {pluralRu(total, ['пара', 'пары', 'пар'])}
          </p>
          <ul className="divide-y divide-border rounded-md border border-border">
            {pairs.map((p) => (
              <li key={`${p.a.id}-${p.b.id}`} className="flex flex-col gap-2 px-3 py-3 sm:flex-row sm:items-center">
                <div className="grid min-w-0 flex-1 gap-1 sm:grid-cols-2 sm:gap-4">
                  <Side d={p.a} />
                  <Side d={p.b} />
                </div>
                <MergeAuthorsDialog a={p.a} b={p.b} />
              </li>
            ))}
          </ul>
          {hasNextPage ? (
            <Button variant="outline" className="w-full" onClick={() => void fetchNextPage()} disabled={isFetchingNextPage}>
              {isFetchingNextPage ? 'Загрузка…' : 'Показать ещё'}
            </Button>
          ) : null}
        </>
      )}
    </article>
  );
}

function Side({ d }: { d: DuplicateAuthor }) {
  return (
    <div className="min-w-0 text-sm">
      <Link to="/authors/$id" params={{ id: String(d.id) }} className="block truncate font-medium hover:underline">
        {d.full_name}
      </Link>
      <span className="text-xs tabular-nums text-muted-foreground">
        {d.book_count} {pluralBooks(d.book_count)}
        {d.years_active ? ` · ${d.years_active.from}–${d.years_active.to}` : ''}
      </span>
    </div>
  );
}
