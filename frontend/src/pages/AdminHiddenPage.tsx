import { Link } from '@tanstack/react-router';
import { EyeOff } from 'lucide-react';
import { AdminTabs } from '@/components/AdminTabs';
import { Button } from '@/components/ui/button';
import { Callout } from '@/components/ui/callout';
import { Skeleton } from '@/components/ui/skeleton';
import { LoadError } from '@/components/LoadError';
import { pluralRu } from '@/lib/format';
import { useHiddenList, useSetHidden } from '@/lib/hiding';

/**
 * AdminHiddenPage — /admin/hidden: авторы и работы, скрытые на инстансе
 * (админ, 0064). Скрытое не видит никто, кроме админа; из избранного и с
 * полок оно пропало молча — «Вернуть» возвращает всё на место. Скрывают на
 * карточке автора или книги в режиме «Править».
 */
export function AdminHiddenPage() {
  const { data, isLoading, error } = useHiddenList();
  const setHidden = useSetHidden();
  const items = data?.items ?? [];

  return (
    <article className="space-y-6">
      <AdminTabs />
      <header className="space-y-1">
        <h1 className="text-2xl font-semibold">Скрытое</h1>
        <p className="text-sm text-muted-foreground text-pretty">
          Авторы и книги, скрытые на этом сервере: их не видит никто, кроме администратора — ни в каталоге и поиске,
          ни в OPDS, ни по прямой ссылке. У скрытого автора скрыты и все книги с ним, включая совместные и сборники.
          В избранном и на полках читателей они пропали молча и вернутся вместе с ними.
        </p>
      </header>

      {isLoading ? (
        <div className="space-y-2">
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-12 w-full" />
        </div>
      ) : error ? (
        <LoadError error={error}>Не удалось загрузить список скрытого.</LoadError>
      ) : items.length === 0 ? (
        <Callout icon={<EyeOff className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
          Ничего не скрыто. Скрыть автора или книгу — на их карточке в режиме «Править».
        </Callout>
      ) : (
        <ul className="divide-y divide-border rounded-md border border-border">
          {items.map((it) => (
            <li key={`${it.kind}-${it.id}`} className="flex flex-wrap items-center justify-between gap-2 px-3 py-2">
              <div className="min-w-0 space-y-0.5">
                <p className="text-sm">
                  <span className="text-muted-foreground">{it.kind === 'author' ? 'Автор' : 'Книга'} · </span>
                  <Link
                    to={it.kind === 'author' ? '/authors/$id' : '/works/$id'}
                    params={{ id: String(it.id) }}
                    className="font-medium hover:underline"
                  >
                    {it.name}
                  </Link>
                </p>
                <p className="text-xs text-muted-foreground">
                  {it.books} {pluralRu(it.books, ['издание', 'издания', 'изданий'])} · скрыто{' '}
                  {new Date(it.hidden_at).toLocaleDateString('ru-RU')}
                  {it.note ? ` · ${it.note}` : ''}
                </p>
              </div>
              <Button
                variant="outline"
                size="sm"
                disabled={setHidden.isPending}
                onClick={() => setHidden.mutate({ kind: it.kind, id: it.id, hide: false })}
              >
                Вернуть
              </Button>
            </li>
          ))}
        </ul>
      )}
    </article>
  );
}
