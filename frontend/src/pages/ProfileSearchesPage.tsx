import { useState } from 'react';
import { Link } from '@tanstack/react-router';
import { Search, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Callout } from '@/components/ui/callout';
import { Skeleton } from '@/components/ui/skeleton';
import { LoadError } from '@/components/LoadError';
import { ProfileTabs } from '@/components/ProfileTabs';
import { useClearSearchLog, useDeleteSearchLogEntry, useSearchLog } from '@/lib/searchLog';
import { formatRelativeDays, pluralBooks, pluralRu } from '@/lib/format';

/**
 * ProfileSearchesPage — «Поиск» (/me/searches): что искали в библиотеке и
 * нашлось ли. Ненайденное — список желаний: когда книга появится при импорте,
 * Главная покажет «Появилось то, что вы искали». Строку и всю историю можно
 * удалить; историю видит только её владелец.
 */
export function ProfileSearchesPage() {
  const log = useSearchLog(200);
  const del = useDeleteSearchLogEntry();
  const clear = useClearSearchLog();
  const [confirmClear, setConfirmClear] = useState(false);

  const retention = log.data?.retention_days ?? 0;
  const items = log.data?.items ?? [];

  return (
    <article className="space-y-6">
      <ProfileTabs />
      <header className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Поиск</h1>
        <p className="text-sm text-pretty text-muted-foreground">
          Что вы искали в библиотеке. Историю видите только вы
          {retention > 0 ? `, хранится ${retention} ${pluralRu(retention, ['день', 'дня', 'дней'])}` : ''}.
          Если книга не нашлась, а потом появится в библиотеке, Главная об этом скажет.
        </p>
      </header>

      {log.isLoading ? (
        <Skeleton className="h-24 w-full max-w-2xl" />
      ) : log.error ? (
        <LoadError error={log.error}>Не удалось загрузить историю.</LoadError>
      ) : retention === 0 ? (
        <Callout icon={<Search className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
          История поиска выключена администратором (`SKRIPTES_SEARCH_LOG_DAYS=0`).
        </Callout>
      ) : items.length === 0 ? (
        <Callout icon={<Search className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
          Пока пусто: здесь появятся запросы из поиска по книгам.
        </Callout>
      ) : (
        <div className="max-w-2xl space-y-3">
          <ul className="divide-y divide-border rounded-md border border-border" data-testid="search-log">
            {items.map((e) => (
              <li key={e.id} className="flex items-start gap-3 px-3 py-2">
                <div className="min-w-0 flex-1">
                  <Link
                    to="/books"
                    search={{ q: e.q }}
                    className="font-medium underline-offset-2 hover:underline"
                  >
                    {e.q}
                  </Link>
                  <p className="text-xs text-muted-foreground">
                    {e.hits === 0 ? 'не нашлось' : `${e.hits} ${pluralBooks(e.hits)}`}
                    {e.clicked_title ? ` · открыли «${e.clicked_title}»` : ''}
                    {' · '}
                    {formatRelativeDays(e.at)}
                  </p>
                </div>
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-7 shrink-0 text-muted-foreground"
                  aria-label={`Удалить запрос «${e.q}»`}
                  disabled={del.isPending}
                  onClick={() => del.mutate(e.id)}
                >
                  <X className="size-4" aria-hidden />
                </Button>
              </li>
            ))}
          </ul>
          {confirmClear ? (
            <div className="flex items-center gap-2">
              <span className="text-sm text-muted-foreground">Удалить всю историю без возврата?</span>
              <Button
                size="sm"
                variant="destructive"
                disabled={clear.isPending}
                onClick={() => {
                  clear.mutate(undefined, { onSettled: () => setConfirmClear(false) });
                }}
              >
                Удалить
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setConfirmClear(false)}>
                Отмена
              </Button>
            </div>
          ) : (
            <Button size="sm" variant="outline" onClick={() => setConfirmClear(true)}>
              Очистить историю
            </Button>
          )}
        </div>
      )}
    </article>
  );
}
